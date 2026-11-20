package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JorTurFer/ldap-explorer/internal/ldapfilter"
	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
)

func builderQuery() *SavedQuery {
	return &SavedQuery{Name: "Alice or Bob", Mode: "builder", Base: "ou=people,dc=example,dc=org", Scope: "one",
		Attrs: "uid, mail", Limit: 100,
		Query: &ldapfilter.Node{Op: "or", Items: []*ldapfilter.Node{
			{Op: "equals", Attr: "uid", Value: "alice"}, {Op: "equals", Attr: "uid", Value: "bob"}}}}
}

func TestSavedQueriesCRUD(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)

	var list []SavedQuery
	if code := call(t, srv, "GET", "/api/queries", nil, &list); code != 200 || len(list) != 0 {
		t.Fatalf("empty list: %d %v", code, list)
	}

	// Save a builder query and a raw one.
	if code := call(t, srv, "POST", "/api/queries", builderQuery(), &list); code != 200 || len(list) != 1 {
		t.Fatalf("save: %d %v", code, list)
	}
	raw := &SavedQuery{Name: "All people", Mode: "raw", Base: "dc=example,dc=org", Scope: "sub", Filter: "(objectClass=inetOrgPerson)", Limit: 500}
	call(t, srv, "POST", "/api/queries", raw, &list)
	if len(list) != 2 || list[0].Name != "Alice or Bob" || list[1].Name != "All people" {
		t.Fatalf("list should be sorted by name: %+v", list)
	}

	// Everything round-trips, and the server stamps the profile.
	got := list[0]
	if got.Profile != "lab" || got.Mode != "builder" || got.Scope != "one" || got.Attrs != "uid, mail" || got.Limit != 100 ||
		got.Query == nil || got.Query.Op != "or" || len(got.Query.Items) != 2 || got.Filter != "" {
		t.Errorf("builder query: %+v", got)
	}
	if list[1].Query != nil || list[1].Filter != "(objectClass=inetOrgPerson)" {
		t.Errorf("raw query: %+v", list[1])
	}

	// Saving under the same name (any case) replaces it; the client cannot choose the profile.
	b := builderQuery()
	b.Name, b.Attrs, b.Profile = "alice or bob", "cn", "someone-else"
	call(t, srv, "POST", "/api/queries", b, &list)
	if len(list) != 2 || list[0].Attrs != "cn" || list[0].Profile != "lab" {
		t.Errorf("replace: %+v", list)
	}

	// Delete (case-insensitive), the other one stays.
	if code := call(t, srv, "DELETE", "/api/queries?name=ALICE+OR+BOB", nil, &list); code != 200 || len(list) != 1 || list[0].Name != "All people" {
		t.Errorf("delete: %d %+v", code, list)
	}
	if code := call(t, srv, "DELETE", "/api/queries?name=nothing", nil, &list); code != 200 || len(list) != 1 {
		t.Errorf("deleting a missing query should be harmless: %d %+v", code, list)
	}
}

func TestSavedQueriesArePerProfile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	var list []SavedQuery

	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	call(t, srv, "POST", "/api/queries", builderQuery(), &list)

	// Another profile does not see it, and can use the same name.
	connectOK(t, srv, Profile{Name: "prod", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/queries", nil, &list); len(list) != 0 {
		t.Errorf("another profile sees %v", list)
	}
	call(t, srv, "POST", "/api/queries", builderQuery(), &list)
	call(t, srv, "DELETE", "/api/queries?name="+"Alice+or+Bob", nil, &list)

	// A connection that is not a saved profile is identified by its URL.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/queries", nil, &list); len(list) != 0 {
		t.Errorf("unsaved connection sees %v", list)
	}
	call(t, srv, "POST", "/api/queries", builderQuery(), &list)
	if list[0].Profile != l.URL {
		t.Errorf("profile key for an unsaved connection: %q", list[0].Profile)
	}

	// "lab" still has its query: deleting from prod did not touch it.
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/queries", nil, &list); len(list) != 1 {
		t.Errorf("lab lost its query: %v", list)
	}
}

func TestSavedQueriesValidation(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "GET", "/api/queries", nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
		t.Errorf("without a session: %d %q", code, e.Error)
	}
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)

	for _, tc := range []struct {
		name string
		edit func(q *SavedQuery)
		msg  string
	}{
		{"no name", func(q *SavedQuery) { q.Name = "   " }, "give the query a name"},
		{"name too long", func(q *SavedQuery) { q.Name = strings.Repeat("x", 101) }, "too long"},
		{"bad mode", func(q *SavedQuery) { q.Mode = "magic" }, "mode must be"},
		{"builder without a query", func(q *SavedQuery) { q.Query = nil }, "add at least one condition"},
		{"raw without a filter", func(q *SavedQuery) { q.Mode, q.Filter = "raw", "  " }, "filter is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := builderQuery()
			tc.edit(q)
			if code := call(t, srv, "POST", "/api/queries", q, &e); code != 400 || !strings.Contains(e.Error, tc.msg) {
				t.Errorf("%d %q, want %q", code, e.Error, tc.msg)
			}
		})
	}
	// An unknown scope is normalised, not rejected.
	q := builderQuery()
	q.Scope = "galaxy"
	var list []SavedQuery
	if code := call(t, srv, "POST", "/api/queries", q, &list); code != 200 || list[0].Scope != "sub" {
		t.Errorf("scope: %d %+v", code, list)
	}
}

func TestSavedQueriesFile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	var list []SavedQuery
	call(t, srv, "POST", "/api/queries", builderQuery(), &list)

	raw, err := os.ReadFile(queriesPath())
	if err != nil {
		t.Fatal(err)
	}
	// Only the search is stored: no credentials of any kind.
	for _, forbidden := range []string{slapdtest.AlicePW, "password", "bindDN", "Password"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("queries.json contains %q", forbidden)
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(queriesPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("queries.json permissions: %v", st.Mode().Perm())
		}
	}
	// Atomic writes leave no temporary files behind.
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(queriesPath()), ".queries-*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	// It lives next to profiles.json.
	if filepath.Dir(queriesPath()) != filepath.Dir(profilesPath()) {
		t.Errorf("queries.json is not next to profiles.json")
	}
}

func TestSavedQueriesCorruptFile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if err := os.MkdirAll(filepath.Dir(queriesPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queriesPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var e errResp
	if code := call(t, srv, "GET", "/api/queries", nil, &e); code != 400 || e.Error == "" {
		t.Errorf("a corrupt file should be reported, not crash: %d %q", code, e.Error)
	}
	// and it must not be overwritten silently by a save
	if code := call(t, srv, "POST", "/api/queries", builderQuery(), &e); code != 400 {
		t.Errorf("saving over a corrupt file: %d", code)
	}
	if b, _ := os.ReadFile(queriesPath()); string(b) != "{not json" {
		t.Errorf("the corrupt file was overwritten: %q", b)
	}
}
