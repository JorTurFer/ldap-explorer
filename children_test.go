package main

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
)

type childrenPageResp struct {
	Children []struct{ DN, RDN string } `json:"children"`
	Cursor   string                     `json:"cursor"`
	Warning  string                     `json:"warning"`
}

func TestChildrenOnePageAtATime(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{URL: l.URL, BindDN: slapdtest.BobDN}, slapdtest.BobPW) // bob has no size limit
	bulk := "ou=bulk," + slapdtest.BaseDN
	get := func(query string) (childrenPageResp, int) {
		var r childrenPageResp
		var e errResp
		code := call(t, srv, "GET", "/api/children?dn="+url.QueryEscape(bulk)+query, nil, &r)
		if code != 200 {
			call(t, srv, "GET", "/api/children?dn="+url.QueryEscape(bulk)+query, nil, &e)
			t.Logf("error: %s", e.Error)
		}
		return r, code
	}

	// The first page is only a page, and says there is more.
	r, code := get("&size=250")
	if code != 200 || len(r.Children) != 250 || r.Cursor == "" {
		t.Fatalf("first page: %d children, cursor %q, code %d", len(r.Children), r.Cursor, code)
	}
	seen := map[string]bool{}
	pages := 1
	for _, c := range r.Children {
		seen[c.DN] = true
	}
	// Following the cursor gives the rest, with no repeats, and ends with no cursor.
	for r.Cursor != "" {
		r, code = get("&cursor=" + r.Cursor)
		if code != 200 {
			t.Fatalf("page %d: code %d", pages+1, code)
		}
		pages++
		for _, c := range r.Children {
			if seen[c.DN] {
				t.Fatalf("%s came twice", c.DN)
			}
			seen[c.DN] = true
		}
	}
	if len(seen) != slapdtest.BulkCount || pages != 3 {
		t.Errorf("%d entries in %d pages, want %d in 3", len(seen), pages, slapdtest.BulkCount)
	}

	// A cursor is single use, and only for the DN it was made for.
	r, _ = get("&size=100")
	if _, code := get("&cursor=" + r.Cursor); code != 200 {
		t.Fatalf("cursor: %d", code)
	}
	if _, code := get("&cursor=" + r.Cursor); code != 400 {
		t.Errorf("a cursor used twice: %d, want 400", code)
	}
	r, _ = get("&size=100")
	var e errResp
	if code := call(t, srv, "GET", "/api/children?dn="+url.QueryEscape("ou=people,"+slapdtest.BaseDN)+"&cursor="+r.Cursor, nil, &e); code != 400 || !strings.Contains(e.Error, "reload") {
		t.Errorf("a cursor for another DN: %d %q", code, e.Error)
	}
	if code := call(t, srv, "GET", "/api/children?dn="+url.QueryEscape(bulk)+"&cursor=nope", nil, &e); code != 400 {
		t.Errorf("an unknown cursor: %d", code)
	}

	// Reconnecting invalidates the cursors (the paging cookie belongs to the old connection).
	r, _ = get("&size=100")
	connectOK(t, srv, Profile{URL: l.URL, BindDN: slapdtest.BobDN}, slapdtest.BobPW)
	if _, code := get("&cursor=" + r.Cursor); code != 400 {
		t.Errorf("a cursor from the previous connection: %d, want 400", code)
	}

	// The page size is kept within bounds; a small directory is a single page without cursor.
	if r, _ := get("&size=1"); len(r.Children) != 10 {
		t.Errorf("size=1 is raised to the minimum: %d", len(r.Children))
	}
	if r, _ := get("&size=99999"); len(r.Children) != 600 && len(r.Children) != maxChildrenPage {
		t.Errorf("size=99999 is capped: %d", len(r.Children))
	}
	var small childrenPageResp
	call(t, srv, "GET", "/api/children?dn="+url.QueryEscape("ou=people,"+slapdtest.BaseDN)+"&size=200", nil, &small)
	if len(small.Children) != 3 || small.Cursor != "" {
		t.Errorf("a small branch: %d children, cursor %q", len(small.Children), small.Cursor)
	}
}

func TestChildrenKeepsAtMostAFewCursors(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{URL: l.URL, BindDN: slapdtest.BobDN}, slapdtest.BobPW)
	var first childrenPageResp
	path := "/api/children?dn=" + url.QueryEscape("ou=bulk,"+slapdtest.BaseDN) + "&size=100"
	call(t, srv, "GET", path, nil, &first)
	for i := 0; i < maxCursors; i++ { // more listings opened and left unfinished push the first one out
		var other childrenPageResp
		call(t, srv, "GET", path, nil, &other)
	}
	var e errResp
	if code := call(t, srv, "GET", path+"&cursor="+first.Cursor, nil, &e); code != 400 {
		t.Errorf("the oldest cursor should have been dropped: %d", code)
	}
	cursors.mu.Lock()
	n := len(cursors.items)
	cursors.mu.Unlock()
	if n > maxCursors {
		t.Errorf("%d cursors kept, at most %d", n, maxCursors)
	}
}

func TestChildrenServerLimitGivesWhatWeHaveAndNoCursor(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW) // alice stops at the server limit (550)
	var r childrenPageResp
	path := "/api/children?dn=" + url.QueryEscape("ou=bulk,"+slapdtest.BaseDN) + "&size=200"
	n := 0
	for i := 0; i < 5; i++ {
		call(t, srv, "GET", path, nil, &r)
		n += len(r.Children)
		if r.Cursor == "" {
			break
		}
		path = "/api/children?dn=" + url.QueryEscape("ou=bulk,"+slapdtest.BaseDN) + "&cursor=" + r.Cursor
	}
	if r.Cursor != "" || n == 0 || n >= slapdtest.BulkCount {
		t.Errorf("with a server limit: %d entries, last cursor %q (warning %q)", n, r.Cursor, r.Warning)
	}
}

// A "light" listing asks for the names only: it is the way out when a page of a huge branch is
// too slow because the server works out hasSubordinates for every entry (issue #7).
func TestChildrenLightListing(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	type child struct {
		DN, RDN       string
		Leaf, Unknown bool
	}
	var r struct {
		Children []child `json:"children"`
	}
	path := "/api/children?dn=" + url.QueryEscape(slapdtest.BaseDN)
	if code := call(t, srv, "GET", path+"&size=50", nil, &r); code != 200 {
		t.Fatalf("normal: %d", code)
	}
	known := 0
	for _, c := range r.Children {
		if !c.Unknown {
			known++
		}
	}
	if known == 0 {
		t.Fatal("a normal listing knows which children are leaves")
	}
	r.Children = nil
	if code := call(t, srv, "GET", path+"&size=50&light=1", nil, &r); code != 200 || len(r.Children) != 5 {
		t.Fatalf("light: %d, %d children", code, len(r.Children))
	}
	for _, c := range r.Children {
		if !c.Unknown || c.Leaf {
			t.Errorf("a light listing says nothing about children: %+v", c)
		}
	}
}

func TestTheServerTimeoutIsLongAndConfigurable(t *testing.T) {
	if ldapTimeout < time.Minute {
		t.Errorf("ldapTimeout %v: a page of a huge branch needs more than 30 s (issue #7)", ldapTimeout)
	}
	if childAttrs(true)[0] != "1.1" || len(childAttrs(true)) != 1 || len(childAttrs(false)) != 3 {
		t.Errorf("attributes: %v / %v", childAttrs(true), childAttrs(false))
	}
}
