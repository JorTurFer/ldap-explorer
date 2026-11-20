package main

// "Which groups is this entry a member of?", answered with what the server's schema allows
// (member, uniqueMember, memberUid): the same logic as the "Groups a user belongs to" recipe.

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"ldap-explorer/internal/ldapfilter"
	"ldap-explorer/internal/recipes"
)

type groupRef struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
}

type groupsResponse struct {
	Groups    []groupRef `json:"groups"`
	Available bool       `json:"available"`        // false: this server's schema has no way to answer it
	Reason    string     `json:"reason,omitempty"` // why, when it is not available
}

// searchBaseFor picks the naming context that contains dn (the longest match), else the first one.
func searchBaseFor(contexts []string, dn string) string {
	best := ""
	for _, nc := range contexts {
		if (sameDN(dn, nc) || strings.HasSuffix(normalizeDN(dn), ","+normalizeDN(nc))) && len(nc) > len(best) {
			best = nc
		}
	}
	if best == "" && len(contexts) > 0 {
		best = contexts[0]
	}
	return best
}

// handleGroups: GET /api/entry/groups?dn=...
func handleGroups(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	dn := r.URL.Query().Get("dn")
	if dn == "" {
		writeErr(w, errors.New("dn is required"))
		return
	}
	env, err := recipeEnv()
	if err != nil {
		writeErr(w, err)
		return
	}
	// The login (memberUid lists logins, not DNs) comes from the entry itself.
	uid := ""
	if res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 30, false,
		"(objectClass=*)", []string{"uid", "sAMAccountName"}, nil)); err == nil && len(res.Entries) > 0 {
		for _, a := range []string{"uid", "sAMAccountName"} {
			if v := res.Entries[0].GetEqualFoldAttributeValue(a); v != "" {
				uid = v
				break
			}
		}
	} else if err != nil {
		writeErr(w, err)
		return
	}
	env.Now = time.Now()
	plan, err := recipes.Apply(env, "user-groups", map[string]string{"dn": dn, "uid": uid})
	if err != nil {
		// The server cannot answer: say so, it is not a failure.
		writeJSON(w, groupsResponse{Groups: []groupRef{}, Available: false, Reason: strings.TrimPrefix(err.Error(), "this server cannot answer it: ")})
		return
	}
	_, idx, schemaErr := getSchema(c)
	opt := ldapfilter.Options{}
	if schemaErr == nil {
		opt.Lookup = func(a string) (ldapfilter.AttrInfo, bool) { i, ok := idx[strings.ToLower(a)]; return i, ok }
	}
	built, errs := ldapfilter.Build(plan.Query, opt)
	if len(errs) > 0 {
		writeErr(w, errs[0])
		return
	}
	info, err := readRootDSE(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	req := searchRequest{Base: searchBaseFor(info.NamingContexts, dn), Filter: built.Filter, Scope: "sub", Attrs: "cn", Limit: 500}
	res, _, err := req.run(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := groupsResponse{Groups: []groupRef{}, Available: true}
	for _, e := range res.Entries {
		name := e.GetEqualFoldAttributeValue("cn")
		if name == "" {
			name = rdnValue(e.DN)
		}
		out.Groups = append(out.Groups, groupRef{DN: e.DN, Name: name})
	}
	sort.Slice(out.Groups, func(i, j int) bool { return strings.ToLower(out.Groups[i].Name) < strings.ToLower(out.Groups[j].Name) })
	writeJSON(w, out)
}
