package main

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"ldap-explorer/internal/slapdtest"
)

// Tests of the discovery endpoints against a real OpenLDAP.

func TestDiscoveryNotConnectedAndBadInput(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	for _, p := range []string{"/api/server", "/api/schema", "/api/sample?base=x"} {
		if code := call(t, srv, "GET", p, nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
			t.Errorf("%s without a session: %d %q", p, code, e.Error)
		}
	}
	l := slapdtest.Start(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	if code := call(t, srv, "GET", "/api/sample", nil, &e); code != 400 || !strings.Contains(e.Error, "base is required") {
		t.Errorf("sample without base: %d %q", code, e.Error)
	}
	for _, lim := range []string{"abc", "0", "-5"} {
		if code := call(t, srv, "GET", "/api/sample?base=x&limit="+lim, nil, &e); code != 400 {
			t.Errorf("sample limit=%s: %d", lim, code)
		}
	}
}

func TestDiscoveryServer(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	var info serverInfo
	if code := call(t, srv, "GET", "/api/server", nil, &info); code != 200 {
		t.Fatalf("server: %d", code)
	}
	if info.Flavor != "openldap" {
		t.Errorf("flavor: %q", info.Flavor)
	}
	if !slices.Contains(info.NamingContexts, slapdtest.BaseDN) {
		t.Errorf("namingContexts: %v", info.NamingContexts)
	}
	if info.SubschemaSubentry == "" {
		t.Error("subschemaSubentry should be published")
	}
	if !slices.Contains(info.SupportedLDAPVersion, "3") {
		t.Errorf("supportedLDAPVersion: %v", info.SupportedLDAPVersion)
	}
	if !slices.Contains(info.SupportedControls, "1.2.840.113556.1.4.319") { // simple paged results
		t.Errorf("paged results control not advertised: %v", info.SupportedControls)
	}
}

func TestDiscoverySchema(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	var sc schemaResponse
	if code := call(t, srv, "GET", "/api/schema", nil, &sc); code != 200 {
		t.Fatalf("schema: %d", code)
	}
	// The UI iterates over these lists: a null (an item with no name, say) would break it.
	for _, a := range sc.Attributes {
		if a.Names == nil {
			t.Errorf("attribute %s has null names", a.OID)
		}
	}
	for _, c := range sc.Classes {
		if c.Names == nil || c.Sup == nil || c.Must == nil || c.May == nil {
			t.Errorf("class %s has a null list: %+v", c.OID, c)
		}
	}
	// Every description published by the real server must parse.
	if len(sc.Errors) != 0 {
		t.Errorf("unparsed schema descriptions: %v", sc.Errors)
	}
	if len(sc.Attributes) < 50 || len(sc.Classes) < 10 {
		t.Fatalf("suspiciously small schema: %d attributes, %d classes", len(sc.Attributes), len(sc.Classes))
	}
	attr := func(name string) schemaAttr {
		for _, a := range sc.Attributes {
			if slices.ContainsFunc(a.Names, func(n string) bool { return strings.EqualFold(n, name) }) {
				return a
			}
		}
		t.Fatalf("attribute %q not in the schema", name)
		return schemaAttr{}
	}
	cn := attr("commonName") // alias
	if cn.Name != "cn" || cn.Kind != "string" || !cn.CanEqual || !cn.CanSubstring || cn.SyntaxName != "Directory String" {
		t.Errorf("cn: %+v", cn)
	}
	if mail := attr("mail"); mail.SyntaxName != "IA5 String" || !mail.CanSubstring {
		t.Errorf("mail: %+v", mail)
	}
	if ct := attr("createTimestamp"); !ct.Operational || ct.Kind != "time" || !ct.CanOrder || !ct.NoUserMod {
		t.Errorf("createTimestamp: %+v", ct)
	}
	if pw := attr("userPassword"); pw.Kind != "binary" {
		t.Errorf("userPassword: %+v", pw)
	}
	if photo := attr("jpegPhoto"); photo.Kind != "binary" {
		t.Errorf("jpegPhoto: %+v", photo)
	}
	// what the schema browser shows: where it derives from and which matching rules apply
	if cn.Sup != "name" || cn.Equality != "caseIgnoreMatch" || cn.Substring != "caseIgnoreSubstringsMatch" {
		t.Errorf("cn inherits its rules from name: sup=%q equality=%q substring=%q", cn.Sup, cn.Equality, cn.Substring)
	}
	if ct := attr("createTimestamp"); ct.Usage != "directoryOperation" || ct.Ordering != "generalizedTimeOrderingMatch" || !ct.SingleValue {
		t.Errorf("createTimestamp: usage=%q ordering=%q single=%v", ct.Usage, ct.Ordering, ct.SingleValue)
	}
	if mail := attr("mail"); mail.Substring != "caseIgnoreIA5SubstringsMatch" || mail.Usage != "" {
		t.Errorf("mail: %+v", mail)
	}
	if mem := attr("member"); mem.Kind != "dn" {
		t.Errorf("member: %+v", mem)
	}
	if a := attr("objectClass"); a.Kind != "string" && a.Kind != "other" {
		t.Errorf("objectClass: %+v", a)
	}

	class := func(name string) schemaClass {
		for _, c := range sc.Classes {
			if strings.EqualFold(c.Name, name) {
				return c
			}
		}
		t.Fatalf("object class %q not in the schema", name)
		return schemaClass{}
	}
	p := class("person")
	if p.Kind != "STRUCTURAL" || !slices.Contains(p.Must, "sn") || !slices.Contains(p.Must, "cn") || !slices.Contains(p.Sup, "top") {
		t.Errorf("person: %+v", p)
	}
	if io := class("inetOrgPerson"); !slices.Contains(io.Sup, "organizationalPerson") || !slices.Contains(io.May, "mail") {
		t.Errorf("inetOrgPerson: %+v", io)
	}
	if class("top").Kind != "ABSTRACT" {
		t.Errorf("top: %+v", class("top"))
	}

	// Cached for the connection, and dropped on reconnect/disconnect.
	sess.mu.Lock()
	cached := sess.schema != nil
	sess.mu.Unlock()
	if !cached {
		t.Error("the schema should be cached after the first request")
	}
	call(t, srv, "POST", "/api/disconnect", nil, nil)
	sess.mu.Lock()
	cleared := sess.schema == nil
	sess.mu.Unlock()
	if !cleared {
		t.Error("the schema cache should be cleared on disconnect")
	}
}

func TestDiscoverySample(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	count := func(us []usage, name string) int {
		for _, u := range us {
			if strings.EqualFold(u.Name, name) {
				return u.Count
			}
		}
		return 0
	}
	var s sampleResponse
	people := url.QueryEscape("ou=people," + slapdtest.BaseDN)
	if code := call(t, srv, "GET", "/api/sample?scope=one&base="+people, nil, &s); code != 200 {
		t.Fatalf("sample: %d", code)
	}
	if s.Sampled != 3 || s.Truncated || s.Warning != "" {
		t.Errorf("sample of people: %+v", s)
	}
	// Only the classes stored in the entries are reported; inherited ones are implicit.
	if got := count(s.ObjectClasses, "inetOrgPerson"); got != 3 {
		t.Errorf("objectClass inetOrgPerson: %d entries, want 3", got)
	}
	if got := count(s.ObjectClasses, "person"); got != 0 {
		t.Errorf("inherited class person should not be counted, got %d", got)
	}
	if count(s.Attributes, "uid") != 3 || count(s.Attributes, "mail") != 2 || count(s.Attributes, "jpegPhoto") != 1 {
		t.Errorf("attribute usage: %+v", s.Attributes)
	}
	// attributes are counted per entry, not per value (bob has two telephone numbers)
	if got := count(s.Attributes, "telephoneNumber"); got != 1 {
		t.Errorf("telephoneNumber: %d, want 1 entry", got)
	}
	// most used first, then alphabetical
	for i := 1; i < len(s.Attributes); i++ {
		a, b := s.Attributes[i-1], s.Attributes[i]
		if a.Count < b.Count || (a.Count == b.Count && strings.ToLower(a.Name) > strings.ToLower(b.Name)) {
			t.Fatalf("attributes not sorted at %d: %v then %v", i, a, b)
		}
	}

	// explicit limit
	bulk := url.QueryEscape("ou=bulk," + slapdtest.BaseDN)
	if code := call(t, srv, "GET", "/api/sample?scope=one&limit=50&base="+bulk, nil, &s); code != 200 || s.Sampled != 50 || !s.Truncated {
		t.Errorf("limit=50: %d %+v", code, s)
	}
	// the server's own size limit (alice: 550) yields a partial sample, not an error
	if code := call(t, srv, "GET", "/api/sample?scope=one&limit=1000&base="+bulk, nil, &s); code != 200 ||
		s.Sampled == 0 || s.Sampled >= slapdtest.BulkCount || !s.Truncated || s.Warning == "" {
		t.Errorf("server size limit: %d sampled=%d truncated=%v warning=%q", code, s.Sampled, s.Truncated, s.Warning)
	}
	// a base that does not exist is an error
	var e errResp
	if code := call(t, srv, "GET", "/api/sample?base="+url.QueryEscape("ou=nothing,"+slapdtest.BaseDN), nil, &e); code != 400 {
		t.Errorf("missing base: %d", code)
	}
}
