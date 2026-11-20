package main

// Per-profile lists the user builds up while working: bookmarked entries and the history
// of searches. Both live next to profiles.json, never hold credentials, are written
// atomically with mode 0600, and a damaged file is reported and never overwritten.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var userdataMu sync.Mutex

func readList[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	var v []T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// ---------- bookmarks ----------

type Bookmark struct {
	Profile string `json:"profile"` // set by the server from the connection
	DN      string `json:"dn"`
	Label   string `json:"label"`
}

const (
	maxBookmarksPerProfile = 200
	maxDNLen               = 2000
	maxLabelLen            = 100
)

func bookmarksPath() string { return filepath.Join(configDir(), "bookmarks.json") }

func sameDN(a, b string) bool { return normalizeDN(a) == normalizeDN(b) }

var dnSpaces = regexp.MustCompile(`\s*([,=+])\s*`)

// normalizeDN is a loose canonical form for comparing DNs: case-insensitive, no spaces
// around , = or +.
func normalizeDN(dn string) string {
	return strings.ToLower(dnSpaces.ReplaceAllString(strings.TrimSpace(dn), "$1"))
}

// rdnValue is the value of the first RDN: a sensible default label for a bookmark.
func rdnValue(dn string) string {
	first := dn
	for i := 0; i < len(dn); i++ {
		if dn[i] == '\\' {
			i++
			continue
		}
		if dn[i] == ',' {
			first = dn[:i]
			break
		}
	}
	if i := strings.IndexByte(first, '='); i >= 0 {
		first = first[i+1:]
	}
	return strings.TrimSpace(first)
}

func handleBookmarks(w http.ResponseWriter, r *http.Request) {
	_, p, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	key := profileKey(p)
	userdataMu.Lock()
	defer userdataMu.Unlock()
	all, err := readList[Bookmark](bookmarksPath())
	if err != nil {
		writeErr(w, err)
		return
	}
	mine := func(list []Bookmark) []Bookmark {
		out := []Bookmark{}
		for _, b := range list {
			if b.Profile == key {
				out = append(out, b)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
		return out
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, mine(all))

	case http.MethodPost: // add, or rename if the DN is already bookmarked
		var b Bookmark
		if err := decode(r, &b); err != nil {
			writeErr(w, err)
			return
		}
		b.DN, b.Label = strings.TrimSpace(b.DN), strings.TrimSpace(b.Label)
		switch {
		case b.DN == "":
			writeErr(w, errors.New("a bookmark needs a DN"))
			return
		case len(b.DN) > maxDNLen:
			writeErr(w, errors.New("the DN is too long"))
			return
		case len([]rune(b.Label)) > maxLabelLen:
			writeErr(w, errors.New("the label is too long (100 characters at most)"))
			return
		}
		if b.Label == "" {
			b.Label = rdnValue(b.DN)
		}
		b.Profile = key
		replaced, count := false, 0
		for i := range all {
			if all[i].Profile != key {
				continue
			}
			count++
			if sameDN(all[i].DN, b.DN) {
				all[i], replaced = b, true
			}
		}
		if !replaced {
			if count >= maxBookmarksPerProfile {
				writeErr(w, errors.New("too many bookmarks (200): remove some first"))
				return
			}
			all = append(all, b)
		}
		if err := writePrivateJSON(bookmarksPath(), all); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, mine(all))

	case http.MethodDelete:
		dn := r.URL.Query().Get("dn")
		out := all[:0]
		for _, b := range all {
			if !(b.Profile == key && sameDN(b.DN, dn)) {
				out = append(out, b)
			}
		}
		if err := writePrivateJSON(bookmarksPath(), out); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, mine(out))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---------- search history ----------

// HistoryItem is a search that was run: the same fields as a saved query, plus when.
type HistoryItem struct {
	SavedQuery
	At      string `json:"at"`      // RFC 3339, set by the server
	Summary string `json:"summary"` // the LDAP filter that was used, to recognise it in a list
}

const (
	maxHistoryPerProfile = 30
	maxHistoryTotal      = 600
)

func historyPath() string { return filepath.Join(configDir(), "history.json") }

// sameSearch says whether two history items are the same search (when it ran does not count).
func sameSearch(a, b HistoryItem) bool {
	qa, _ := json.Marshal(a.Query)
	qb, _ := json.Marshal(b.Query)
	return a.Mode == b.Mode && a.Base == b.Base && a.Scope == b.Scope && a.Attrs == b.Attrs && a.Limit == b.Limit &&
		a.Filter == b.Filter && string(qa) == string(qb)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	_, p, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	key := profileKey(p)
	userdataMu.Lock()
	defer userdataMu.Unlock()
	all, err := readList[HistoryItem](historyPath())
	if err != nil {
		writeErr(w, err)
		return
	}
	mine := func(list []HistoryItem) []HistoryItem { // newest first
		out := []HistoryItem{}
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].Profile == key {
				out = append(out, list[i])
			}
		}
		return out
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, mine(all))

	case http.MethodPost:
		var h HistoryItem
		if err := decode(r, &h); err != nil {
			writeErr(w, err)
			return
		}
		if err := validateSearch(&h.SavedQuery); err != nil {
			writeErr(w, err)
			return
		}
		h.Name, h.Profile = "", key
		h.At = time.Now().UTC().Format(time.RFC3339)
		if len(h.Summary) > 500 {
			h.Summary = h.Summary[:500]
		}
		// the same search again moves to the top instead of being listed twice
		out := all[:0]
		for _, old := range all {
			if !(old.Profile == key && sameSearch(old, h)) {
				out = append(out, old)
			}
		}
		out = append(out, h)
		// keep the newest few per profile, and a cap on the whole file
		count := 0
		kept := make([]HistoryItem, 0, len(out))
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Profile == key {
				if count++; count > maxHistoryPerProfile {
					continue
				}
			}
			kept = append(kept, out[i])
		}
		if len(kept) > maxHistoryTotal {
			kept = kept[:maxHistoryTotal]
		}
		for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
			kept[i], kept[j] = kept[j], kept[i]
		}
		if err := writePrivateJSON(historyPath(), kept); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, mine(kept))

	case http.MethodDelete: // clear this profile's history
		out := all[:0]
		for _, h := range all {
			if h.Profile != key {
				out = append(out, h)
			}
		}
		if err := writePrivateJSON(historyPath(), out); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, []HistoryItem{})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
