package main

// Server discovery (read-only): what kind of server this is, its schema, and
// which attributes are actually used in a branch. This feeds the query
// assistant: it lets the UI suggest attributes and operators without the user
// having to know them.

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/JorTurFer/ldap-explorer/internal/ldapfilter"
	"github.com/JorTurFer/ldap-explorer/internal/ldapschema"
	"github.com/go-ldap/ldap/v3"
)

// ---------- RootDSE / server flavor ----------

type serverInfo struct {
	Flavor               string   `json:"flavor"` // openldap, 389ds, ad or unknown
	VendorName           string   `json:"vendorName,omitempty"`
	VendorVersion        string   `json:"vendorVersion,omitempty"`
	NamingContexts       []string `json:"namingContexts"`
	DefaultNamingContext string   `json:"defaultNamingContext,omitempty"`
	SubschemaSubentry    string   `json:"subschemaSubentry,omitempty"`
	SupportedLDAPVersion []string `json:"supportedLDAPVersion,omitempty"`
	SupportedControls    []string `json:"supportedControls,omitempty"`
	SupportedExtensions  []string `json:"supportedExtensions,omitempty"`
	SupportedSASL        []string `json:"supportedSASLMechanisms,omitempty"`
}

func readRootDSE(c *ldap.Conn) (*serverInfo, error) {
	res, err := c.Search(ldap.NewSearchRequest("", ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 10, false, "(objectClass=*)", []string{"*", "+"}, nil))
	if err != nil {
		return nil, err
	}
	if len(res.Entries) == 0 {
		return nil, errors.New("the server returned no RootDSE")
	}
	e := res.Entries[0]
	get := e.GetEqualFoldAttributeValues
	info := &serverInfo{
		VendorName:           first(get("vendorName")),
		VendorVersion:        first(get("vendorVersion")),
		NamingContexts:       nonNil(get("namingContexts")),
		DefaultNamingContext: first(get("defaultNamingContext")),
		SubschemaSubentry:    first(get("subschemaSubentry")),
		SupportedLDAPVersion: get("supportedLDAPVersion"),
		SupportedControls:    get("supportedControl"),
		SupportedExtensions:  get("supportedExtension"),
		SupportedSASL:        get("supportedSASLMechanisms"),
	}
	info.Flavor = detectFlavor(e, info)
	return info, nil
}

func detectFlavor(e *ldap.Entry, info *serverInfo) string {
	if len(e.GetEqualFoldAttributeValues("rootDomainNamingContext")) > 0 ||
		len(e.GetEqualFoldAttributeValues("isGlobalCatalogReady")) > 0 {
		return "ad"
	}
	if strings.Contains(strings.ToLower(info.VendorName), "389") {
		return "389ds"
	}
	for _, oc := range e.GetEqualFoldAttributeValues("objectClass") {
		if strings.EqualFold(oc, "OpenLDAProotDSE") {
			return "openldap"
		}
	}
	return "unknown"
}

func first(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func handleServer(w http.ResponseWriter, _ *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	info, err := readRootDSE(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, info)
}

// ---------- schema ----------

type schemaAttr struct {
	Name         string   `json:"name"`
	Names        []string `json:"names"`
	OID          string   `json:"oid"`
	Desc         string   `json:"desc,omitempty"`
	Syntax       string   `json:"syntax,omitempty"`
	SyntaxName   string   `json:"syntaxName,omitempty"`
	Kind         string   `json:"kind"` // string, integer, boolean, time, dn, binary, other
	CanEqual     bool     `json:"canEqual"`
	CanOrder     bool     `json:"canOrder"`
	CanSubstring bool     `json:"canSubstring"`
	RulesUnknown bool     `json:"rulesUnknown,omitempty"` // the schema declares no matching rules: the operators above are a guess from the syntax
	SingleValue  bool     `json:"singleValue"`
	Operational  bool     `json:"operational"`
	NoUserMod    bool     `json:"noUserModification"`
	Obsolete     bool     `json:"obsolete,omitempty"`
	Sup          string   `json:"sup,omitempty"`      // the attribute type it derives from
	Equality     string   `json:"equality,omitempty"` // matching rules (inherited ones included)
	Ordering     string   `json:"ordering,omitempty"`
	Substring    string   `json:"substring,omitempty"`
	Usage        string   `json:"usage,omitempty"` // userApplications (default) or an operational kind
}

type schemaClass struct {
	Name     string   `json:"name"`
	Names    []string `json:"names"`
	OID      string   `json:"oid"`
	Desc     string   `json:"desc,omitempty"`
	Kind     string   `json:"kind"` // STRUCTURAL, AUXILIARY, ABSTRACT
	Sup      []string `json:"sup"`
	Must     []string `json:"must"`
	May      []string `json:"may"`
	Obsolete bool     `json:"obsolete,omitempty"`
}

type schemaResponse struct {
	Attributes []schemaAttr  `json:"attributes"`
	Classes    []schemaClass `json:"objectClasses"`
	Errors     []string      `json:"errors,omitempty"` // descriptions that could not be parsed
}

func loadSchema(c *ldap.Conn) (*schemaResponse, error) {
	info, err := readRootDSE(c)
	if err != nil {
		return nil, err
	}
	dn := info.SubschemaSubentry
	if dn == "" {
		return nil, errors.New("the server does not publish its schema (no subschemaSubentry in the RootDSE)")
	}
	res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 60, false, "(objectClass=*)", []string{"attributeTypes", "objectClasses"}, nil))
	if err != nil {
		return nil, fmt.Errorf("reading the schema: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, errors.New("the schema entry is empty or not readable by this user")
	}
	e := res.Entries[0]
	s, errs := ldapschema.Parse(e.GetEqualFoldAttributeValues("attributeTypes"), e.GetEqualFoldAttributeValues("objectClasses"))
	out := &schemaResponse{Attributes: []schemaAttr{}, Classes: []schemaClass{}}
	for _, err := range errs {
		out.Errors = append(out.Errors, err.Error())
	}
	for _, a := range s.Attributes {
		out.Attributes = append(out.Attributes, schemaAttr{
			Name: a.Name(), Names: nonNil(a.Names), OID: a.OID, Desc: a.Desc, Syntax: a.Syntax, SyntaxName: a.SyntaxName(),
			Kind: a.Kind(), CanEqual: a.CanEqual(), CanOrder: a.CanOrder(), CanSubstring: a.CanSubstring(), RulesUnknown: a.RulesUnknown(),
			SingleValue: a.SingleValue, Operational: a.Operational(), NoUserMod: a.NoUserMod, Obsolete: a.Obsolete,
			Sup: a.Sup, Equality: a.Equality, Ordering: a.Ordering, Substring: a.Substr, Usage: a.Usage,
		})
	}
	for _, c := range s.Classes {
		out.Classes = append(out.Classes, schemaClass{
			Name: c.Name(), Names: nonNil(c.Names), OID: c.OID, Desc: c.Desc, Kind: c.Kind,
			Sup: nonNil(c.Sup), Must: nonNil(c.Must), May: nonNil(c.May), Obsolete: c.Obsolete,
		})
	}
	return out, nil
}

// getSchema returns the schema of the current connection, loading it on first
// use. It is cached for the life of the connection (schemas are large and rarely
// change), together with an index of attributes for the filter builder.
func getSchema(c *ldap.Conn) (*schemaResponse, map[string]ldapfilter.AttrInfo, error) {
	sc, idx, _, err := getSchemaFull(c)
	return sc, idx, err
}

func getSchemaFull(c *ldap.Conn) (*schemaResponse, map[string]ldapfilter.AttrInfo, map[string]bool, error) {
	sess.mu.Lock()
	sc, idx, classes := sess.schema, sess.attrs, sess.classes
	sess.mu.Unlock()
	if sc != nil {
		return sc, idx, classes, nil
	}
	sc, err := loadSchema(c)
	if err != nil {
		return nil, nil, nil, err
	}
	idx = indexAttributes(sc)
	classes = indexClasses(sc)
	sess.mu.Lock()
	if sess.conn == c { // the session may have been replaced meanwhile
		sess.schema, sess.attrs, sess.classes = sc, idx, classes
	}
	sess.mu.Unlock()
	return sc, idx, classes, nil
}

func indexClasses(sc *schemaResponse) map[string]bool {
	m := make(map[string]bool, len(sc.Classes)*2)
	for _, c := range sc.Classes {
		m[strings.ToLower(c.OID)] = true
		for _, n := range c.Names {
			m[strings.ToLower(n)] = true
		}
	}
	return m
}

// indexAttributes maps every name and OID (lower-cased) to what the filter
// builder needs to know.
func indexAttributes(sc *schemaResponse) map[string]ldapfilter.AttrInfo {
	idx := make(map[string]ldapfilter.AttrInfo, len(sc.Attributes)*2)
	for _, a := range sc.Attributes {
		info := ldapfilter.AttrInfo{Name: a.Name, Kind: a.Kind, CanEqual: a.CanEqual, CanOrder: a.CanOrder, CanSubstring: a.CanSubstring}
		idx[strings.ToLower(a.OID)] = info
		for _, n := range a.Names {
			idx[strings.ToLower(n)] = info
		}
	}
	return idx
}

func handleSchema(w http.ResponseWriter, _ *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	sc, _, err := getSchema(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, sc)
}

// ---------- attribute usage sampling ----------

type usage struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type sampleResponse struct {
	Sampled       int     `json:"sampled"`
	Truncated     bool    `json:"truncated"` // the sample limit (or a server limit) was reached
	ObjectClasses []usage `json:"objectClasses"`
	Attributes    []usage `json:"attributes"`
	Warning       string  `json:"warning,omitempty"`
}

const (
	defaultSampleLimit = 200
	maxSampleLimit     = 1000
)

// handleSample looks at up to `limit` entries below `base` and reports which
// object classes and attributes are really in use, so the user can discover the
// structure of a branch without knowing the schema.
func handleSample(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	base := q.Get("base")
	if base == "" {
		writeErr(w, errors.New("base is required"))
		return
	}
	limit := defaultSampleLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, errors.New("limit must be a positive number"))
			return
		}
		limit = min(n, maxSampleLimit)
	}
	scope := ldap.ScopeWholeSubtree
	switch q.Get("scope") {
	case "one":
		scope = ldap.ScopeSingleLevel
	case "base":
		scope = ldap.ScopeBaseObject
	}
	res, err := c.Search(ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases,
		limit, 60, false, "(objectClass=*)", []string{"*"}, nil))
	out := &sampleResponse{ObjectClasses: []usage{}, Attributes: []usage{}}
	if err != nil {
		if res == nil || len(res.Entries) == 0 {
			writeErr(w, err)
			return
		}
		out.Warning = err.Error() // e.g. size limit exceeded: report what we got
		out.Truncated = true
	}
	classes, attrs := newCounter(), newCounter()
	for _, e := range res.Entries {
		for _, a := range e.Attributes {
			if strings.EqualFold(a.Name, "objectClass") {
				seen := map[string]bool{}
				for _, v := range a.Values {
					if k := strings.ToLower(v); !seen[k] {
						seen[k] = true
						classes.add(v)
					}
				}
			}
			attrs.add(a.Name)
		}
	}
	out.Sampled = len(res.Entries)
	if out.Sampled >= limit {
		out.Truncated = true
	}
	out.ObjectClasses, out.Attributes = classes.sorted(), attrs.sorted()
	writeJSON(w, out)
}

// counter counts names case-insensitively, remembering the first spelling.
type counter struct {
	n    map[string]int
	name map[string]string
}

func newCounter() *counter { return &counter{n: map[string]int{}, name: map[string]string{}} }

func (c *counter) add(s string) {
	k := strings.ToLower(s)
	if _, ok := c.name[k]; !ok {
		c.name[k] = s
	}
	c.n[k]++
}

// sorted lists the names by descending count, then alphabetically.
func (c *counter) sorted() []usage {
	out := make([]usage, 0, len(c.n))
	for k, n := range c.n {
		out = append(out, usage{Name: c.name[k], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
