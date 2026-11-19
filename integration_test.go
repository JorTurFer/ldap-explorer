package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// Tests against a real OpenLDAP (ephemeral slapd, see slapd_test.go).

func alice(l *testLDAP) Profile {
	return Profile{Name: "alice", URL: l.URL, BindDN: testAliceDN}
}

func TestLDAPConnectAndRoots(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)

	roots := connectOK(t, srv, alice(l), testAlicePW)
	if len(roots) == 0 || roots[0] != testBaseDN {
		t.Errorf("roots (namingContexts from the RootDSE): %v", roots)
	}
	// An explicit BaseDN goes first
	p := alice(l)
	p.BaseDN = "ou=people," + testBaseDN
	if roots := connectOK(t, srv, p, testAlicePW); roots[0] != p.BaseDN {
		t.Errorf("roots with BaseDN: %v", roots)
	}

	var e errResp
	if code := call(t, srv, "POST", "/api/connect", connectReq{alice(l), "wrong"}, &e); code != 400 || !strings.Contains(e.Error, "bind") {
		t.Errorf("wrong bind: %d %q", code, e.Error)
	}
	// a failed bind must not bring down the already open session
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &entryResp{}); code != 200 {
		t.Errorf("previous session lost after failed bind: %d", code)
	}
	if code := call(t, srv, "POST", "/api/disconnect", nil, nil); code != 200 {
		t.Errorf("disconnect: %d", code)
	}
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
		t.Errorf("after disconnect: %d %q", code, e.Error)
	}
}

func TestLDAPAnonymousAndAdminBind(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{URL: l.URL}, "")
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &entryResp{}); code != 200 {
		t.Errorf("anonymous read: %d", code)
	}
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testAdminDN}, testAdminPW)
}

func TestLDAPPasswordFromKeychain(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	p := alice(l)
	p.UseKeychain = true
	// save profile with password -> keychain; connecting without a password uses it
	var list []Profile
	if code := call(t, srv, "POST", "/api/profiles", connectReq{p, testAlicePW}, &list); code != 200 {
		t.Fatalf("save profile: %d", code)
	}
	connectOK(t, srv, p, "")

	// Password stored by a previous version (plain text, same account): still valid.
	p2 := Profile{Name: "legacy", URL: l.URL, BindDN: testAliceDN + "", UseKeychain: true}
	p2.URL = l.URL + "/" // a different account
	if err := keyring.Set(keychainService, keychainAccount(p2), testAlicePW); err != nil {
		t.Fatal(err)
	}
	connectOK(t, srv, p2, "")
}

func TestLDAPTLS(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	var e errResp

	// self-signed cert: must fail verification...
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: l.LDAPSURL, BindDN: testAliceDN}, testAlicePW}, &e); code != 400 {
		t.Errorf("ldaps without skipVerify should fail with a self-signed cert: %d", code)
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: l.URL, StartTLS: true, BindDN: testAliceDN}, testAlicePW}, &e); code != 400 {
		t.Errorf("StartTLS without skipVerify should fail: %d", code)
	}
	// ...and work with skipVerify
	connectOK(t, srv, Profile{URL: l.LDAPSURL, SkipVerify: true, BindDN: testAliceDN}, testAlicePW)
	connectOK(t, srv, Profile{URL: l.URL, StartTLS: true, SkipVerify: true, BindDN: testAliceDN}, testAlicePW)
}

func TestLDAPTreeChildren(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)

	type child struct {
		DN, RDN       string
		Leaf, Unknown bool
	}
	var r struct {
		Children []child `json:"children"`
		Warning  string  `json:"warning"`
	}
	get := func(dn string) {
		r.Children, r.Warning = nil, ""
		if code := call(t, srv, "GET", "/api/children?dn="+url.QueryEscape(dn), nil, &r); code != 200 {
			t.Fatalf("children %s: %d", dn, code)
		}
	}

	get(testBaseDN)
	var rdns []string
	byRDN := map[string]child{}
	for _, c := range r.Children {
		rdns = append(rdns, c.RDN)
		byRDN[c.RDN] = c
	}
	want := []string{"ou=bulk", "ou=empty", "ou=groups", "ou=people"} // sorted
	if strings.Join(rdns, ",") != strings.Join(want, ",") {
		t.Fatalf("children of the root: %v", rdns)
	}
	if !byRDN["ou=empty"].Leaf {
		t.Error("ou=empty should be marked as a leaf (hasSubordinates=FALSE)")
	}
	if byRDN["ou=people"].Leaf || byRDN["ou=people"].Unknown {
		t.Errorf("ou=people should have children: %+v", byRDN["ou=people"])
	}

	get("ou=people," + testBaseDN)
	if len(r.Children) != 3 || !r.Children[0].Leaf {
		t.Errorf("children of people: %+v", r.Children)
	}

	// paging: more entries (600) than the page size (500). Bob has no size
	// limit on the server, so he must see them all without a warning.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testBobDN}, testBobPW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) != bulkCount || r.Warning != "" {
		t.Errorf("paging: %d children (expected %d), warning=%q", len(r.Children), bulkCount, r.Warning)
	}

	// Zoe only sees more than 100 results if the search is paged: checks
	// that children uses the paging control.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testZoeDN}, testZoePW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) != bulkCount || r.Warning != "" {
		t.Errorf("paging (user with a limit of 100, unpaged): %d children, warning=%q", len(r.Children), r.Warning)
	}

	// Alice has the server limit (550): partial results + warning, not an error.
	connectOK(t, srv, alice(l), testAlicePW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) == 0 || len(r.Children) >= bulkCount || r.Warning == "" {
		t.Errorf("server limit: %d children, warning=%q", len(r.Children), r.Warning)
	}
}

func TestLDAPEntry(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)
	q := func(dn string, op bool) (entryResp, int) {
		var e entryResp
		path := "/api/entry?dn=" + url.QueryEscape(dn)
		if op {
			path += "&operational=1"
		}
		code := call(t, srv, "GET", path, nil, &e)
		return e, code
	}

	e, code := q(testAliceDN, false)
	if code != 200 || e.DN == "" {
		t.Fatalf("entry: %d %+v", code, e)
	}
	if e.Attrs[0].Name != "objectClass" {
		t.Errorf("objectClass should come first: %v", e.Attrs[0].Name)
	}
	if a := e.attr("mail"); a == nil || a.Values[0] != "alice@example.org" {
		t.Errorf("mail: %+v", a)
	}
	if a := e.attr("jpegPhoto"); a == nil || !a.Binary || !strings.Contains(a.Values[0], "binary") {
		t.Errorf("jpegPhoto should be summarized as binary: %+v", a)
	}
	if e.attr("entryUUID") != nil {
		t.Error("without operational=1 there should be no operational attributes")
	}
	if e, _ := q(testAliceDN, true); e.attr("entryUUID") == nil {
		t.Error("with operational=1 entryUUID should be present")
	}

	// non-ASCII (UTF-8)
	if e, _ := q("uid=zoe,ou=people,"+testBaseDN, false); e.attr("cn") == nil || e.attr("cn").Values[0] != "Zoë Ñandú" {
		t.Errorf("cn UTF-8: %+v", e.attr("cn"))
	}

	var er errResp
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape("uid=nobody,"+testBaseDN), nil, &er); code != 400 {
		t.Errorf("nonexistent entry: %d", code)
	}
}

type searchResp struct {
	Entries []Entry `json:"entries"`
	Warning string  `json:"warning"`
}

func TestLDAPSearch(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)
	people := "ou=people," + testBaseDN

	search := func(body map[string]any) searchResp {
		t.Helper()
		var r searchResp
		if code := call(t, srv, "POST", "/api/search", body, &r); code != 200 {
			t.Fatalf("search %v: %d", body, code)
		}
		return r
	}
	searchErr := func(body map[string]any) string {
		t.Helper()
		var e errResp
		if code := call(t, srv, "POST", "/api/search", body, &e); code != 400 {
			t.Fatalf("search %v: expected 400, got %d", body, code)
		}
		return e.Error
	}

	// scopes
	for scope, want := range map[string]int{"base": 1, "one": 3, "sub": 4, "": 4} {
		if r := search(map[string]any{"base": people, "scope": scope}); len(r.Entries) != want {
			t.Errorf("scope %q: %d entries, expected %d", scope, len(r.Entries), want)
		}
	}

	// filter + requested attributes (separated by comma or space)
	r := search(map[string]any{"base": testBaseDN, "filter": "(uid=alice)", "attrs": "mail, sn"})
	if len(r.Entries) != 1 || r.Entries[0].DN != testAliceDN {
		t.Fatalf("filter uid=alice: %+v", r.Entries)
	}
	names := map[string]bool{}
	for _, a := range r.Entries[0].Attrs {
		names[strings.ToLower(a.Name)] = true
	}
	if len(names) != 2 || !names["mail"] || !names["sn"] {
		t.Errorf("returned attributes: %v", names)
	}

	// empty filter = (objectClass=*)
	if r := search(map[string]any{"base": people, "scope": "one", "filter": ""}); len(r.Entries) != 3 {
		t.Errorf("empty filter: %d", len(r.Entries))
	}
	// filter with wildcard
	if r := search(map[string]any{"base": testBaseDN, "filter": "(&(objectClass=inetOrgPerson)(mail=*@example.org))"}); len(r.Entries) != 2 {
		t.Errorf("compound filter: %d", len(r.Entries))
	}

	// errors
	if msg := searchErr(map[string]any{"base": people, "filter": "(uid="}); !strings.Contains(msg, "invalid filter") {
		t.Errorf("invalid filter: %q", msg)
	}
	searchErr(map[string]any{"base": "ou=nothing," + testBaseDN})

	// limit requested by the user: returns what there is + warning
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 10})
	if len(r.Entries) != 10 {
		t.Errorf("limit=10: %d entries", len(r.Entries))
	}
	if r.Warning == "" {
		t.Error("limit=10 should return a size limit warning")
	}
	// server limit (sizelimit 550 < 600): partial results + warning, not an error
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 10000})
	if len(r.Entries) == 0 || len(r.Entries) >= bulkCount || r.Warning == "" {
		t.Errorf("server limit: %d entries, warning=%q", len(r.Entries), r.Warning)
	}
	// within the server limit, no warning and paging (500 per page)
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "filter": "(uid=u05*)", "limit": 1000})
	if len(r.Entries) != 100 || r.Warning != "" {
		t.Errorf("uid=u05*: %d entries, warning=%q", len(r.Entries), r.Warning)
	}

	// paging in search: Zoe only sees >100 results if the search is paged
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testZoeDN}, testZoePW)
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 1000})
	if len(r.Entries) != bulkCount || r.Warning != "" {
		t.Errorf("paged search: %d entries, warning=%q", len(r.Entries), r.Warning)
	}
}
