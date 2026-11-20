package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// What the server is doing for the UI right now, so the status bar can say more than
// "waiting": how long each request has been running and, for paged searches, how many
// entries have arrived so far. The UI tags each request with an X-Op header (a random id it
// makes up); GET /api/activity lists the running ones. Nothing here is stored or logged: an
// operation exists only while its request runs, and it holds no search text, only counters.

type operation struct {
	started time.Time
	entries atomic.Int64
	pages   atomic.Int64
	lastAt  atomic.Int64 // unix ms of the last sign of life (a page arrived)
}

type opCtxKey struct{}

var (
	runningOps sync.Map // id -> *operation
	opIDFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// trackOperation tags the request with an operation when the UI asked for one (X-Op).
func trackOperation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Op")
		if !opIDFormat.MatchString(id) {
			next.ServeHTTP(w, r)
			return
		}
		o := &operation{started: time.Now()}
		o.lastAt.Store(o.started.UnixMilli())
		runningOps.Store(id, o)
		defer runningOps.Delete(id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), opCtxKey{}, o)))
	})
}

func operationFrom(ctx context.Context) *operation {
	o, _ := ctx.Value(opCtxKey{}).(*operation)
	return o
}

// page records a page of results (safe on a nil operation: requests without a tag).
func (o *operation) page(entries int) {
	if o == nil {
		return
	}
	o.entries.Add(int64(entries))
	o.pages.Add(1)
	o.lastAt.Store(time.Now().UnixMilli())
}

type opStatus struct {
	ID        string `json:"id"`
	ElapsedMs int64  `json:"elapsedMs"`
	Entries   int64  `json:"entries"`
	Pages     int64  `json:"pages"`
	QuietMs   int64  `json:"quietMs"` // time since the last page arrived (or since the start)
}

func handleActivity(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	out := []opStatus{}
	runningOps.Range(func(k, v any) bool {
		o := v.(*operation)
		out = append(out, opStatus{ID: k.(string), ElapsedMs: now.Sub(o.started).Milliseconds(),
			Entries: o.entries.Load(), Pages: o.pages.Load(), QuietMs: now.UnixMilli() - o.lastAt.Load()})
		return true
	})
	writeJSON(w, map[string]any{"ops": out})
}

// pagedSearch is ldap.Conn.SearchWithPaging with two additions: it reports every page to the
// operation (progress) and it stops asking for more pages when the request is cancelled (the
// user pressed Cancel, or closed the tab). Like the original, when something goes wrong it
// returns what was received so far together with the error.
func pagedSearch(ctx context.Context, c *ldap.Conn, sr *ldap.SearchRequest, size uint32) (*ldap.SearchResult, error) {
	op := operationFrom(ctx)
	paging := ldap.NewControlPaging(size)
	sr.Controls = append(sr.Controls, paging)
	all := &ldap.SearchResult{}
	more := true
	abandon := false
	var failure error
	for more {
		if err := ctx.Err(); err != nil {
			failure, abandon = err, true
			break
		}
		res, err := c.Search(sr)
		if res != nil {
			all.Entries = append(all.Entries, res.Entries...)
			all.Referrals = append(all.Referrals, res.Referrals...)
			all.Controls = append(all.Controls, res.Controls...)
			op.page(len(res.Entries))
		} else if err == nil {
			err = ldap.NewError(ldap.ErrorNetwork, errors.New("ldap: packet not received"))
		}
		if err != nil {
			failure = err
			break
		}
		more = false
		if pc := ldap.FindControl(res.Controls, ldap.ControlTypePaging); pc != nil {
			if cookie := pc.(*ldap.ControlPaging).Cookie; len(cookie) > 0 {
				paging.SetCookie(cookie)
				more = true
			}
		}
	}
	if abandon { // stopped in the middle: tell the server to forget the rest
		paging.PagingSize = 0
		_, _ = c.Search(sr)
	}
	return all, failure
}
