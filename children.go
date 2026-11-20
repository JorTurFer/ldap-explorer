package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// The children of a DN, for the tree.
//
// A directory can have hundreds of thousands of entries under one node, and reading them all
// just to draw a tree is what made opening such a node take minutes. So the tree asks for one
// page at a time (?size=N) and, when there is more, the answer carries a "cursor": the id of
// the server-side paging position, which the next request sends back (?cursor=ID) to get the
// following page. Without ?size the whole list is returned, as before.
//
// A cursor holds the LDAP paging cookie, which is only valid on the connection that made it,
// for the same search. At most maxCursors are kept (the oldest is abandoned on the server) and
// they expire; using one that is gone gives a clear "reload this branch" error.

const (
	defaultChildrenPage = 200
	maxChildrenPage     = 1000
	maxCursors          = 6
	cursorTTL           = 10 * time.Minute
)

type childNode struct {
	DN      string `json:"dn"`
	RDN     string `json:"rdn"`
	Leaf    bool   `json:"leaf"`
	Unknown bool   `json:"unknown"`
}

type cursor struct {
	dn      string
	conn    *ldap.Conn
	sr      *ldap.SearchRequest
	paging  *ldap.ControlPaging
	created time.Time
}

type cursorStore struct {
	mu    sync.Mutex
	items map[string]*cursor
	order []string
}

var cursors = &cursorStore{items: map[string]*cursor{}}

var errCursorGone = errors.New("this list is out of date (the connection changed or it was open too long): reload the branch")

// take removes and returns a cursor, if it belongs to this connection and this DN. While a
// request uses it nobody else can, so two clicks cannot replay the same paging cookie.
func (s *cursorStore) take(id string, conn *ldap.Conn, dn string) (*cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cu, ok := s.items[id]
	if !ok || cu.conn != conn || !strings.EqualFold(cu.dn, dn) || time.Since(cu.created) > cursorTTL {
		return nil, errCursorGone
	}
	s.remove(id)
	return cu, nil
}

func (s *cursorStore) remove(id string) {
	delete(s.items, id)
	for i, o := range s.order {
		if o == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// put keeps a cursor and returns its id, abandoning the oldest ones beyond the limit.
func (s *cursorStore) put(cu *cursor) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	cu.created = time.Now()
	s.mu.Lock()
	s.items[id] = cu
	s.order = append(s.order, id)
	var drop []*cursor
	for len(s.order) > maxCursors {
		drop = append(drop, s.items[s.order[0]])
		s.remove(s.order[0])
	}
	for _, o := range append([]string(nil), s.order...) { // expired ones
		if c := s.items[o]; c != nil && time.Since(c.created) > cursorTTL {
			drop = append(drop, c)
			s.remove(o)
		}
	}
	s.mu.Unlock()
	for _, d := range drop {
		d.abandon()
	}
	return id
}

// abandon tells the server to forget the rest of the listing (best effort).
func (cu *cursor) abandon() {
	cu.paging.PagingSize = 0
	_, _ = cu.conn.Search(cu.sr)
}

// childrenPage reads one page of the children of dn. next is the cursor for the following
// page ("" when this was the last one).
func childrenPage(ctx context.Context, c *ldap.Conn, dn string, size uint32, cursorID string) (entries []*ldap.Entry, next, warning string, err error) {
	var cu *cursor
	if cursorID != "" {
		if cu, err = cursors.take(cursorID, c, dn); err != nil {
			return nil, "", "", err
		}
	} else {
		paging := ldap.NewControlPaging(size)
		sr := ldap.NewSearchRequest(dn, ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
			0, 60, false, "(objectClass=*)", []string{"1.1", "hasSubordinates", "numSubordinates"}, []ldap.Control{paging})
		cu = &cursor{dn: dn, conn: c, sr: sr, paging: paging}
	}
	res, err := c.Search(cu.sr)
	if res != nil {
		entries = res.Entries
		operationFrom(ctx).page(len(entries))
	}
	if err != nil {
		if len(entries) == 0 {
			return nil, "", "", err
		}
		return entries, "", err.Error(), nil // e.g. the server's size limit: what we have, no more pages
	}
	if pc := ldap.FindControl(res.Controls, ldap.ControlTypePaging); pc != nil {
		if cookie := pc.(*ldap.ControlPaging).Cookie; len(cookie) > 0 {
			cu.paging.SetCookie(cookie)
			next = cursors.put(cu)
		}
	}
	return entries, next, "", nil
}

func toChildren(entries []*ldap.Entry) []childNode {
	out := make([]childNode, 0, len(entries))
	for _, e := range entries {
		ch := childNode{DN: e.DN, RDN: rdnOf(e.DN)}
		hs := e.GetAttributeValue("hasSubordinates")
		ns := e.GetAttributeValue("numSubordinates")
		switch {
		case strings.EqualFold(hs, "FALSE") || ns == "0":
			ch.Leaf = true
		case hs == "" && ns == "":
			ch.Unknown = true
		}
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].RDN) < strings.ToLower(out[j].RDN) })
	return out
}

func handleChildren(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	dn := q.Get("dn")
	resp := map[string]any{}
	var entries []*ldap.Entry
	var warning string

	if sizeText := q.Get("size"); sizeText != "" || q.Get("cursor") != "" {
		size, _ := strconv.Atoi(sizeText)
		if size <= 0 {
			size = defaultChildrenPage
		}
		size = min(max(size, 10), maxChildrenPage)
		var next string
		entries, next, warning, err = childrenPage(r.Context(), c, dn, uint32(size), q.Get("cursor"))
		if err != nil {
			writeErr(w, err)
			return
		}
		resp["cursor"] = next
	} else {
		sr := ldap.NewSearchRequest(dn, ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
			0, 60, false, "(objectClass=*)", []string{"1.1", "hasSubordinates", "numSubordinates"}, nil)
		res, perr := pagedSearch(r.Context(), c, sr, 500)
		if perr != nil && (res == nil || len(res.Entries) == 0) {
			writeErr(w, perr)
			return
		}
		entries = res.Entries
		if perr != nil {
			warning = perr.Error()
		}
	}
	resp["children"] = toChildren(entries)
	if warning != "" {
		resp["warning"] = warning
	}
	writeJSON(w, resp)
}
