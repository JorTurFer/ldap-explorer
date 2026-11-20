package main

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
)

func TestGroupsOfAnEntry(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	get := func(dn string) groupsResponse {
		t.Helper()
		var g groupsResponse
		if code := call(t, srv, "GET", "/api/entry/groups?dn="+url.QueryEscape(dn), nil, &g); code != 200 {
			t.Fatalf("%s: %d", dn, code)
		}
		return g
	}
	names := func(g groupsResponse) []string {
		var n []string
		for _, x := range g.Groups {
			n = append(n, x.Name)
		}
		return n
	}
	people := "ou=people," + slapdtest.BaseDN

	// alice is in admins by DN (member) and in staff by login (memberUid)
	g := get(slapdtest.AliceDN)
	if !g.Available || !slices.Equal(names(g), []string{"admins", "staff"}) {
		t.Errorf("alice: %+v", g)
	}
	if g.Groups[0].DN != "cn=admins,ou=groups,"+slapdtest.BaseDN {
		t.Errorf("each group carries its DN: %+v", g.Groups[0])
	}
	// bob is only in staff (memberUid)
	if g := get("uid=bob," + people); !slices.Equal(names(g), []string{"staff"}) {
		t.Errorf("bob: %+v", g)
	}
	// zoe is in none: an empty list, still "available"
	if g := get("uid=zoe," + people); !g.Available || len(g.Groups) != 0 {
		t.Errorf("zoe: %+v", g)
	}
	// the DN may be typed in another case
	if g := get(strings.ToUpper(slapdtest.AliceDN)); len(g.Groups) < 1 {
		t.Errorf("an upper-case DN should still find the DN-based group: %+v", g)
	}
	// errors
	var e errResp
	if code := call(t, srv, "GET", "/api/entry/groups", nil, &e); code != 400 || !strings.Contains(e.Error, "required") {
		t.Errorf("no dn: %d %q", code, e.Error)
	}
	if code := call(t, srv, "GET", "/api/entry/groups?dn="+url.QueryEscape("uid=nobody,"+people), nil, &e); code != 400 {
		t.Errorf("a missing entry: %d", code)
	}
}

func TestGroupsNeedAConnection(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "GET", "/api/entry/groups?dn=x", nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
		t.Errorf("%d %q", code, e.Error)
	}
}

func TestSearchBaseFor(t *testing.T) {
	ctxs := []string{"dc=example,dc=org", "dc=corp", "ou=deep,dc=corp"}
	for dn, want := range map[string]string{
		"uid=a,ou=people,dc=example,dc=org": "dc=example,dc=org",
		"UID=A,OU=PEOPLE,DC=EXAMPLE,DC=ORG": "dc=example,dc=org",
		"dc=example,dc=org":                 "dc=example,dc=org",
		"cn=x,ou=deep,dc=corp":              "ou=deep,dc=corp", // the longest context wins
		"cn=y,dc=corp":                      "dc=corp",
		"cn=z,dc=elsewhere":                 "dc=example,dc=org", // none matches: the first
	} {
		if got := searchBaseFor(ctxs, dn); got != want {
			t.Errorf("searchBaseFor(%q) = %q, want %q", dn, got, want)
		}
	}
	if searchBaseFor(nil, "dc=x") != "" {
		t.Error("no contexts: empty base")
	}
}
