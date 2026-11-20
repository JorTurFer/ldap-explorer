package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
)

func bulkRequest() *ldap.SearchRequest {
	return ldap.NewSearchRequest("ou=bulk,"+slapdtest.BaseDN, ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
		0, 60, false, "(objectClass=*)", []string{"1.1"}, nil)
}

func bobConn(t *testing.T) *ldap.Conn {
	t.Helper()
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{Name: "bob", URL: l.URL, BindDN: slapdtest.BobDN}, slapdtest.BobPW)
	c, _, err := sess.get()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPagedSearchReportsEveryPage(t *testing.T) {
	c := bobConn(t)
	op := &operation{started: time.Now()}
	ctx := context.WithValue(context.Background(), opCtxKey{}, op)
	res, err := pagedSearch(ctx, c, bulkRequest(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != slapdtest.BulkCount {
		t.Fatalf("entries: %d, want %d", len(res.Entries), slapdtest.BulkCount)
	}
	if op.entries.Load() != int64(slapdtest.BulkCount) || op.pages.Load() != 6 {
		t.Errorf("progress: %d entries in %d pages, want %d in 6", op.entries.Load(), op.pages.Load(), slapdtest.BulkCount)
	}
}

func TestPagedSearchWithoutAnOperationStillWorks(t *testing.T) {
	c := bobConn(t)
	res, err := pagedSearch(context.Background(), c, bulkRequest(), 500)
	if err != nil || len(res.Entries) != slapdtest.BulkCount {
		t.Fatalf("%d entries, %v", len(res.Entries), err)
	}
}

func TestPagedSearchStopsWhenCancelled(t *testing.T) {
	c := bobConn(t)
	op := &operation{started: time.Now()}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), opCtxKey{}, op))
	defer cancel()
	go func() { // cancel as soon as a few pages have arrived
		for op.pages.Load() < 3 {
			time.Sleep(50 * time.Microsecond)
		}
		cancel()
	}()
	res, err := pagedSearch(ctx, c, bulkRequest(), 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
	if len(res.Entries) < 3 || len(res.Entries) >= slapdtest.BulkCount {
		t.Errorf("a cancelled search returns what it had (%d entries), not everything", len(res.Entries))
	}
	// the connection is still good for the next request after abandoning the paging
	if _, err := pagedSearch(context.Background(), c, bulkRequest(), 500); err != nil {
		t.Errorf("the connection should still work: %v", err)
	}
}

func TestActivityListsRunningRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := trackOperation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operationFrom(r.Context()).page(42)
		started <- struct{}{}
		<-release
	}))
	srv := httptest.NewServer(slow)
	defer srv.Close()
	var done atomic.Bool
	go func() {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req.Header.Set("X-Op", "op-1_a")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
		done.Store(true)
	}()
	<-started
	list := func() []opStatus {
		rec := httptest.NewRecorder()
		handleActivity(rec, httptest.NewRequest("GET", "/api/activity", nil))
		var r struct{ Ops []opStatus }
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Ops
	}
	ops := list()
	if len(ops) != 1 || ops[0].ID != "op-1_a" || ops[0].Entries != 42 || ops[0].Pages != 1 {
		t.Fatalf("running: %+v", ops)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for len(list()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a finished request is still listed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestActivityIgnoresBadOperationIDs(t *testing.T) {
	for _, id := range []string{"", "has space", "semi;colon", strings.Repeat("a", 65), "../x"} {
		var seen *operation
		h := trackOperation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = operationFrom(r.Context()) }))
		req := httptest.NewRequest("GET", "/", nil)
		if id != "" {
			req.Header.Set("X-Op", id)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if seen != nil {
			t.Errorf("X-Op %q was accepted", id)
		}
	}
}

func TestActivityNeedsTheToken(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/api/activity", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("without a token: %d", resp.StatusCode)
	}
	var out struct{ Ops []opStatus }
	if code := call(t, srv, "GET", "/api/activity", nil, &out); code != 200 || out.Ops == nil {
		t.Errorf("with a token: %d %+v (an empty list must be [], not null)", code, out)
	}
}
