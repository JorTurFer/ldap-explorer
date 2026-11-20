package main

// Exports as LDIF, the standard text format for LDAP data. Values are written as the
// server holds them (binary-safe), not the display summary the entry view shows.

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JorTurFer/ldap-explorer/internal/ldif"
	"github.com/go-ldap/ldap/v3"
)

// toLDIF converts search results to LDIF entries: objectClass first, then the other
// attributes alphabetically, like the entry view.
func toLDIF(entries []*ldap.Entry) []ldif.Entry {
	out := make([]ldif.Entry, 0, len(entries))
	for _, e := range entries {
		le := ldif.Entry{DN: e.DN}
		attrs := append([]*ldap.EntryAttribute(nil), e.Attributes...)
		sort.SliceStable(attrs, func(i, j int) bool {
			ai, aj := strings.EqualFold(attrs[i].Name, "objectClass"), strings.EqualFold(attrs[j].Name, "objectClass")
			if ai != aj {
				return ai
			}
			return strings.ToLower(attrs[i].Name) < strings.ToLower(attrs[j].Name)
		})
		for _, a := range attrs {
			le.Attrs = append(le.Attrs, ldif.Attr{Name: a.Name, Values: a.ByteValues})
		}
		out = append(out, le)
	}
	return out
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ldifFilename makes a safe file name from the first RDN value of a DN.
func ldifFilename(dn string) string {
	name := dn
	if i := strings.IndexByte(name, ','); i >= 0 {
		name = name[:i]
	}
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Trim(unsafeFilename.ReplaceAllString(name, "_"), "_.")
	if name == "" {
		name = "entry"
	}
	return name + ".ldif"
}

func writeLDIF(w http.ResponseWriter, filename, warning string, entries []*ldap.Entry) {
	var buf bytes.Buffer
	if err := ldif.Encode(&buf, toLDIF(entries)); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if warning != "" {
		w.Header().Set("X-Warning", warning) // the export is partial, e.g. a server size limit
	}
	_, _ = w.Write(buf.Bytes())
}

// handleEntryLDIF: GET /api/entry/ldif?dn=...&operational=1
func handleEntryLDIF(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	dn := r.URL.Query().Get("dn")
	if dn == "" {
		writeErr(w, errors.New("dn is required"))
		return
	}
	attrs := []string{"*"}
	if r.URL.Query().Get("operational") == "1" {
		attrs = append(attrs, "+")
	}
	res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 30, false, "(objectClass=*)", attrs, nil))
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(res.Entries) == 0 {
		writeErr(w, errors.New("entry not found"))
		return
	}
	writeLDIF(w, ldifFilename(dn), "", res.Entries)
}

// handleSearchLDIF: POST /api/search/ldif with the same body as /api/search.
func handleSearchLDIF(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	var req searchRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	res, warning, err := req.run(r.Context(), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeLDIF(w, "ldap-explorer-"+time.Now().UTC().Format("2006-01-02-150405")+".ldif", warning, res.Entries)
}
