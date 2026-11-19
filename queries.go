package main

// Saved queries, per connection profile, in queries.json next to profiles.json.
// A saved query is only the search itself (base, scope, filter or builder tree,
// attributes, limit): never a password or anything about credentials.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"ldapnav/internal/ldapfilter"
)

type SavedQuery struct {
	Name    string           `json:"name"`
	Profile string           `json:"profile"` // set by the server from the connection, not trusted from the client
	Mode    string           `json:"mode"`    // builder | raw
	Base    string           `json:"base"`
	Scope   string           `json:"scope"`
	Attrs   string           `json:"attrs"`
	Limit   int              `json:"limit"`
	Query   *ldapfilter.Node `json:"query,omitempty"`  // mode builder
	Filter  string           `json:"filter,omitempty"` // mode raw
}

const maxQueryNameLen = 100

var queriesMu sync.Mutex

func queriesPath() string {
	return filepath.Join(filepath.Dir(profilesPath()), "queries.json")
}

// profileKey identifies the connection a query belongs to: the profile name, or
// the URL when the connection was not saved as a profile.
func profileKey(p Profile) string {
	if p.Name != "" {
		return p.Name
	}
	return p.URL
}

func loadQueries() ([]SavedQuery, error) {
	b, err := os.ReadFile(queriesPath())
	if errors.Is(err, os.ErrNotExist) {
		return []SavedQuery{}, nil
	}
	if err != nil {
		return nil, err
	}
	var qs []SavedQuery
	if err := json.Unmarshal(b, &qs); err != nil {
		return nil, err
	}
	return qs, nil
}

// saveQueries writes atomically: a crash cannot leave a half-written file.
func saveQueries(qs []SavedQuery) error {
	p := queriesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(qs, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(p), ".queries-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// forProfile returns the queries of one connection, sorted by name.
func forProfile(all []SavedQuery, key string) []SavedQuery {
	out := []SavedQuery{}
	for _, q := range all {
		if q.Profile == key {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func handleQueries(w http.ResponseWriter, r *http.Request) {
	_, p, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	key := profileKey(p)
	queriesMu.Lock()
	defer queriesMu.Unlock()
	all, err := loadQueries()
	if err != nil {
		writeErr(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, forProfile(all, key))

	case http.MethodPost: // save or replace by name
		var q SavedQuery
		if err := decode(r, &q); err != nil {
			writeErr(w, err)
			return
		}
		q.Name = strings.TrimSpace(q.Name)
		if err := validateSaved(&q); err != nil {
			writeErr(w, err)
			return
		}
		q.Profile = key
		replaced := false
		for i := range all {
			if all[i].Profile == key && strings.EqualFold(all[i].Name, q.Name) {
				all[i], replaced = q, true
			}
		}
		if !replaced {
			all = append(all, q)
		}
		if err := saveQueries(all); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, forProfile(all, key))

	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		out := all[:0]
		for _, q := range all {
			if !(q.Profile == key && strings.EqualFold(q.Name, name)) {
				out = append(out, q)
			}
		}
		if err := saveQueries(out); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, forProfile(out, key))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func validateSaved(q *SavedQuery) error {
	switch {
	case q.Name == "":
		return errors.New("give the query a name")
	case len([]rune(q.Name)) > maxQueryNameLen:
		return errors.New("the name is too long (100 characters at most)")
	}
	switch q.Mode {
	case "builder":
		if q.Query == nil {
			return errors.New("there is nothing to save: add at least one condition")
		}
		q.Filter = ""
	case "raw":
		if strings.TrimSpace(q.Filter) == "" {
			return errors.New("there is nothing to save: the filter is empty")
		}
		q.Query = nil
	default:
		return errors.New("mode must be builder or raw")
	}
	switch q.Scope {
	case "sub", "one", "base":
	default:
		q.Scope = "sub"
	}
	return nil
}
