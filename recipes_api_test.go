package main

import (
	"slices"
	"strings"
	"testing"

	"ldapnav/internal/recipes"
	"ldapnav/internal/slapdtest"
)

func TestRecipesAgainstRealServer(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	var list []recipes.Info
	if code := call(t, srv, "GET", "/api/recipes", nil, &list); code != 200 || len(list) < 10 {
		t.Fatalf("recipes: %d (%d)", code, len(list))
	}
	byID := map[string]recipes.Info{}
	for _, r := range list {
		byID[r.ID] = r
	}
	// What this OpenLDAP schema can and cannot answer.
	for id, want := range map[string]bool{
		"find-person": true, "people-without-email": true, "people-by-department": true, "group-members": true,
		"user-groups": true, "recently-created": true, "recently-modified": true, "by-class": true,
		"with-attribute": true, "missing-attribute": true,
		"locked-accounts": false, "expired-accounts": false,
	} {
		if byID[id].Available != want {
			t.Errorf("%s: available=%v, want %v (%s)", id, byID[id].Available, want, byID[id].Reason)
		}
	}
	if r := byID["locked-accounts"].Reason; !strings.Contains(r, "nsAccountLock") {
		t.Errorf("locked-accounts should say what is missing: %q", r)
	}

	// Apply a recipe, build its filter through the API and run it.
	run := func(id string, params map[string]string, base string) (recipes.Plan, []string, searchResp) {
		t.Helper()
		var plan recipes.Plan
		if code := call(t, srv, "POST", "/api/recipes/"+id, map[string]any{"params": params}, &plan); code != 200 {
			var e errResp
			call(t, srv, "POST", "/api/recipes/"+id, map[string]any{"params": params}, &e)
			t.Fatalf("recipe %s: %d %s", id, code, e.Error)
		}
		var f filterOK
		if code := call(t, srv, "POST", "/api/filter", map[string]any{"query": plan.Query}, &f); code != 200 {
			t.Fatalf("recipe %s produced a query the builder rejects: %d", id, code)
		}
		var res searchResp
		if code := call(t, srv, "POST", "/api/search", map[string]any{"base": base, "filter": f.Filter, "scope": plan.Scope, "attrs": plan.Attrs, "limit": 2000}, &res); code != 200 {
			t.Fatalf("recipe %s: search with %s failed: %d", id, f.Filter, code)
		}
		var dns []string
		for _, e := range res.Entries {
			dns = append(dns, strings.SplitN(e.DN, ",", 2)[0])
		}
		slices.Sort(dns)
		return plan, dns, res
	}
	people := "ou=people," + slapdtest.BaseDN
	base := slapdtest.BaseDN

	_, dns, _ := run("find-person", map[string]string{"text": "ali"}, base)
	if !slices.Equal(dns, []string{"uid=alice"}) {
		t.Errorf("find-person: %v", dns)
	}
	_, dns, _ = run("find-person", map[string]string{"text": "BOB@example"}, base) // by email, any case
	if !slices.Equal(dns, []string{"uid=bob"}) {
		t.Errorf("find-person by email: %v", dns)
	}
	_, dns, _ = run("people-without-email", nil, people)
	if !slices.Equal(dns, []string{"uid=zoe"}) {
		t.Errorf("people-without-email: %v", dns)
	}
	if _, dns, _ = run("people-by-department", map[string]string{"text": "Sales"}, people); len(dns) != 0 {
		t.Errorf("people-by-department: %v", dns)
	}

	plan, dns, res := run("group-members", map[string]string{"group": "admin"}, base)
	if !slices.Equal(dns, []string{"cn=admins"}) {
		t.Errorf("group-members: %v", dns)
	}
	if plan.Attrs != "cn, member, uniqueMember, memberUid" {
		t.Errorf("group-members attrs: %q", plan.Attrs)
	}
	var members []string
	for _, a := range res.Entries[0].Attrs {
		if a.Name == "member" {
			members = a.Values
		}
	}
	if !slices.Equal(members, []string{slapdtest.AliceDN}) {
		t.Errorf("group-members should return the member list: %v", members)
	}

	_, dns, _ = run("user-groups", map[string]string{"dn": slapdtest.AliceDN, "uid": "alice"}, base)
	if !slices.Equal(dns, []string{"cn=admins", "cn=staff"}) { // member (DN) and memberUid (login)
		t.Errorf("user-groups alice: %v", dns)
	}
	_, dns, _ = run("user-groups", map[string]string{"dn": "uid=bob,ou=people," + slapdtest.BaseDN, "uid": "bob"}, base)
	if !slices.Equal(dns, []string{"cn=staff"}) {
		t.Errorf("user-groups bob: %v", dns)
	}
	_, dns, _ = run("user-groups", map[string]string{"dn": slapdtest.AliceDN}, base) // without the login, only DN membership
	if !slices.Equal(dns, []string{"cn=admins"}) {
		t.Errorf("user-groups alice by DN only: %v", dns)
	}

	_, dns, _ = run("recently-created", map[string]string{"days": "1"}, people)
	if !slices.Contains(dns, "uid=alice") || len(dns) < 3 {
		t.Errorf("recently-created: %v", dns)
	}
	if _, dns, _ = run("recently-modified", nil, people); !slices.Contains(dns, "uid=zoe") { // default 7 days
		t.Errorf("recently-modified: %v", dns)
	}
	_, dns, _ = run("by-class", map[string]string{"class": "posixGroup"}, base)
	if !slices.Equal(dns, []string{"cn=staff"}) {
		t.Errorf("by-class: %v", dns)
	}
	_, dns, _ = run("with-attribute", map[string]string{"attribute": "memberUid"}, base)
	if !slices.Equal(dns, []string{"cn=staff"}) {
		t.Errorf("with-attribute: %v", dns)
	}
	_, dns, _ = run("missing-attribute", map[string]string{"class": "inetOrgPerson", "attribute": "mail"}, people)
	if !slices.Equal(dns, []string{"uid=zoe"}) {
		t.Errorf("missing-attribute: %v", dns)
	}
}

func TestRecipesErrors(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "GET", "/api/recipes", nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
		t.Errorf("without a session: %d %q", code, e.Error)
	}
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	for _, tc := range []struct {
		name, id string
		params   map[string]string
		code     int
		msg      string
	}{
		{"unknown", "nope", nil, 404, "unknown recipe"},
		{"required parameter", "find-person", map[string]string{"text": ""}, 400, "is required"},
		{"bad days", "recently-created", map[string]string{"days": "soon"}, 400, "between 1 and 36500"},
		{"not answerable here", "locked-accounts", nil, 400, "cannot answer it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := call(t, srv, "POST", "/api/recipes/"+tc.id, map[string]any{"params": tc.params}, &e); code != tc.code || !strings.Contains(e.Error, tc.msg) {
				t.Errorf("%d %q, want %d %q", code, e.Error, tc.code, tc.msg)
			}
		})
	}
}

// The plans of every available recipe must always be accepted by the filter builder.
func TestEveryAvailableRecipeBuildsAValidFilter(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	var list []recipes.Info
	call(t, srv, "GET", "/api/recipes", nil, &list)
	for _, r := range list {
		if !r.Available {
			continue
		}
		params := map[string]string{}
		for _, p := range r.Params {
			switch p.Kind {
			case "class":
				params[p.Name] = "inetOrgPerson"
			case "attribute":
				params[p.Name] = "mail"
			case "dn":
				params[p.Name] = slapdtest.AliceDN
			case "days":
				params[p.Name] = "3"
			default:
				params[p.Name] = "a"
			}
		}
		var plan recipes.Plan
		if code := call(t, srv, "POST", "/api/recipes/"+r.ID, map[string]any{"params": params}, &plan); code != 200 {
			t.Errorf("%s: apply failed (%d)", r.ID, code)
			continue
		}
		var f filterOK
		if code := call(t, srv, "POST", "/api/filter", map[string]any{"query": plan.Query}, &f); code != 200 {
			t.Errorf("%s: its query is rejected by the filter builder", r.ID)
		}
	}
}
