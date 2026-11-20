package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ldap-explorer/internal/ldapfilter"
	"ldap-explorer/internal/slapdtest"
)

// ---------- bookmarks ----------

func TestBookmarks(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)

	var list []Bookmark
	if code := call(t, srv, "GET", "/api/bookmarks", nil, &list); code != 200 || len(list) != 0 {
		t.Fatalf("empty: %d %v", code, list)
	}
	zoe := "uid=zoe,ou=people," + slapdtest.BaseDN
	call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: slapdtest.AliceDN}, &list)
	call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: zoe, Label: "Zoë from sales"}, &list)
	if len(list) != 2 || list[0].Label != "alice" || list[1].Label != "Zoë from sales" || list[0].Profile != "lab" {
		t.Fatalf("a default label from the RDN, sorted by label: %+v", list)
	}
	// the same DN in any case/spacing renames, it does not duplicate
	call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: strings.ToUpper(zoe), Label: "Zoe"}, &list)
	if len(list) != 2 || list[1].Label != "Zoe" {
		t.Errorf("rename: %+v", list)
	}
	call(t, srv, "DELETE", "/api/bookmarks?dn="+zoe, nil, &list)
	if len(list) != 1 || list[0].DN != slapdtest.AliceDN {
		t.Errorf("delete: %+v", list)
	}
	call(t, srv, "DELETE", "/api/bookmarks?dn=nothing", nil, &list) // harmless
	if len(list) != 1 {
		t.Errorf("deleting an unknown DN: %+v", list)
	}

	// each profile has its own
	connectOK(t, srv, Profile{Name: "prod", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/bookmarks", nil, &list); len(list) != 0 {
		t.Errorf("another profile sees %v", list)
	}
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/bookmarks", nil, &list); len(list) != 1 {
		t.Errorf("lab lost its bookmark: %v", list)
	}
}

func TestBookmarkValidationAndFile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "GET", "/api/bookmarks", nil, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
		t.Errorf("not connected: %d %q", code, e.Error)
	}
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	for name, b := range map[string]Bookmark{
		"no dn":      {DN: "  "},
		"dn too big": {DN: strings.Repeat("x", 2001)},
		"long label": {DN: "cn=a", Label: strings.Repeat("l", 101)},
	} {
		if code := call(t, srv, "POST", "/api/bookmarks", b, &e); code != 400 {
			t.Errorf("%s should be refused: %d", name, code)
		}
	}
	var list []Bookmark
	for i := 0; i < 200; i++ {
		if code := call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: fmt.Sprintf("cn=n%d,dc=x", i)}, &list); code != 200 {
			t.Fatalf("bookmark %d: %d", i, code)
		}
	}
	if code := call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: "cn=one-too-many,dc=x"}, &e); code != 400 || !strings.Contains(e.Error, "too many") {
		t.Errorf("the 201st: %d %q", code, e.Error)
	}
	// renaming one when full still works
	if code := call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: "cn=n5,dc=x", Label: "five"}, &list); code != 200 {
		t.Errorf("rename at the limit: %d", code)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(bookmarksPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("permissions: %v", st.Mode().Perm())
		}
	}
	// a damaged file is reported and left alone
	if err := os.WriteFile(bookmarksPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := call(t, srv, "GET", "/api/bookmarks", nil, &e); code != 400 {
		t.Errorf("damaged file: %d", code)
	}
	if code := call(t, srv, "POST", "/api/bookmarks", Bookmark{DN: "cn=x"}, &e); code != 400 {
		t.Errorf("saving over a damaged file: %d", code)
	}
	if b, _ := os.ReadFile(bookmarksPath()); string(b) != "{broken" {
		t.Errorf("the damaged file was overwritten: %q", b)
	}
}

func TestRDNValueAndSameDN(t *testing.T) {
	for dn, want := range map[string]string{
		"uid=alice,ou=people,dc=x": "alice", `cn=Smith\, John,ou=p`: `Smith\, John`, "dc=example": "example", "noequals": "noequals", "": "",
	} {
		if got := rdnValue(dn); got != want {
			t.Errorf("rdnValue(%q) = %q, want %q", dn, got, want)
		}
	}
	if !sameDN("UID=Alice , OU=People,dc=x", "uid=alice,ou=people,dc=x") || sameDN("uid=a,dc=x", "uid=b,dc=x") {
		t.Error("sameDN")
	}
}

// ---------- history ----------

func hist(filter string) HistoryItem {
	return HistoryItem{SavedQuery: SavedQuery{Mode: "raw", Base: slapdtest.BaseDN, Scope: "sub", Filter: filter, Limit: 500}, Summary: filter}
}

func TestSearchHistory(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	var list []HistoryItem
	if code := call(t, srv, "GET", "/api/history", nil, &list); code != 200 || len(list) != 0 {
		t.Fatalf("empty: %d %v", code, list)
	}
	for _, f := range []string{"(uid=a)", "(uid=b)", "(uid=c)"} {
		if code := call(t, srv, "POST", "/api/history", hist(f), &list); code != 200 {
			t.Fatalf("add %s: %d", f, code)
		}
	}
	if len(list) != 3 || list[0].Summary != "(uid=c)" || list[2].Summary != "(uid=a)" {
		t.Fatalf("newest first: %+v", list)
	}
	if list[0].Profile != "lab" || list[0].At == "" || list[0].Name != "" {
		t.Errorf("the server stamps profile and time: %+v", list[0])
	}
	// the same search again moves to the top; it is not listed twice
	call(t, srv, "POST", "/api/history", hist("(uid=a)"), &list)
	if len(list) != 3 || list[0].Summary != "(uid=a)" || list[1].Summary != "(uid=c)" {
		t.Errorf("dedupe: %+v", list)
	}
	// builder searches keep their conditions
	b := HistoryItem{SavedQuery: SavedQuery{Mode: "builder", Base: "dc=x", Scope: "one", Limit: 10,
		Query: &ldapfilter.Node{Op: "equals", Attr: "uid", Value: "a"}}, Summary: "(uid=a)"}
	call(t, srv, "POST", "/api/history", b, &list)
	if len(list) != 4 || list[0].Query == nil || list[0].Query.Value != "a" {
		t.Errorf("builder item: %+v", list[0])
	}
	// clear
	call(t, srv, "DELETE", "/api/history", nil, &list)
	call(t, srv, "GET", "/api/history", nil, &list)
	if len(list) != 0 {
		t.Errorf("clear: %v", list)
	}
}

func TestHistoryIsCappedAndPerProfile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	var list []HistoryItem
	for i := 0; i < 45; i++ {
		call(t, srv, "POST", "/api/history", hist(fmt.Sprintf("(uid=u%02d)", i)), &list)
	}
	if len(list) != 30 || list[0].Summary != "(uid=u44)" || list[29].Summary != "(uid=u15)" {
		t.Errorf("only the newest 30 are kept: %d, first %s, last %s", len(list), list[0].Summary, list[len(list)-1].Summary)
	}
	connectOK(t, srv, Profile{Name: "prod", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/history", nil, &list); len(list) != 0 {
		t.Errorf("another profile sees the history: %d", len(list))
	}
	call(t, srv, "POST", "/api/history", hist("(cn=prod)"), &list)
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	call(t, srv, "DELETE", "/api/history", nil, &list) // clearing lab leaves prod alone
	connectOK(t, srv, Profile{Name: "prod", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	if call(t, srv, "GET", "/api/history", nil, &list); len(list) != 1 {
		t.Errorf("prod lost its history: %v", list)
	}
}

func TestHistoryValidationAndFile(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "GET", "/api/history", nil, &e); code != 400 {
		t.Errorf("not connected: %d", code)
	}
	connectOK(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}, slapdtest.AlicePW)
	for name, h := range map[string]HistoryItem{
		"bad mode":      {SavedQuery: SavedQuery{Mode: "x"}},
		"empty raw":     {SavedQuery: SavedQuery{Mode: "raw", Filter: " "}},
		"empty builder": {SavedQuery: SavedQuery{Mode: "builder"}},
	} {
		if code := call(t, srv, "POST", "/api/history", h, &e); code != 400 {
			t.Errorf("%s should be refused: %d", name, code)
		}
	}
	long := hist("(uid=x)")
	long.Summary = strings.Repeat("s", 900)
	var list []HistoryItem
	call(t, srv, "POST", "/api/history", long, &list)
	if len(list[0].Summary) != 500 {
		t.Errorf("long summaries are cut: %d", len(list[0].Summary))
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(historyPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("permissions: %v", st.Mode().Perm())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(historyPath()), ".write-*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	_ = os.WriteFile(historyPath(), []byte("{broken"), 0o600)
	if code := call(t, srv, "GET", "/api/history", nil, &e); code != 400 {
		t.Errorf("damaged file: %d", code)
	}
	if b, _ := os.ReadFile(historyPath()); string(b) != "{broken" {
		t.Errorf("the damaged file was overwritten: %q", b)
	}
}
