package main

// The exact bytes of one attribute value: for previewing photos, saving a certificate
// or copying a binary value as base64, which the entry view only summarises.

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// maxValueBytes keeps one huge value from being loaded into the browser.
const maxValueBytes = 8 << 20

// extensionFor guesses a file extension from what the bytes are.
func extensionFor(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 && !strings.HasPrefix(contentType, "text/") {
		return exts[0]
	}
	return ".bin"
}

// handleValue: GET /api/entry/value?dn=...&attr=...&i=0[&download=1]
func handleValue(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	dn, attr := q.Get("dn"), q.Get("attr")
	if dn == "" || attr == "" {
		writeErr(w, errors.New("dn and attr are required"))
		return
	}
	index := 0
	if v := q.Get("i"); v != "" {
		if index, err = strconv.Atoi(v); err != nil || index < 0 {
			writeErr(w, errors.New("i must be a non-negative number"))
			return
		}
	}
	res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 30, false, "(objectClass=*)", []string{attr}, nil))
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(res.Entries) == 0 {
		writeErr(w, errors.New("entry not found"))
		return
	}
	for _, a := range res.Entries[0].Attributes {
		if !strings.EqualFold(a.Name, attr) {
			continue
		}
		if index >= len(a.ByteValues) {
			writeErr(w, fmt.Errorf("%s has %d value(s): there is no value number %d", a.Name, len(a.ByteValues), index))
			return
		}
		v := a.ByteValues[index]
		if len(v) > maxValueBytes {
			writeErr(w, fmt.Errorf("this value is %d bytes: too large to load here (limit %d)", len(v), maxValueBytes))
			return
		}
		ct := http.DetectContentType(v)
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if q.Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s%s"`, unsafeFilename.ReplaceAllString(a.Name, "_"), extensionFor(ct)))
		}
		_, _ = w.Write(v)
		return
	}
	writeErr(w, fmt.Errorf("the entry has no attribute %q", attr))
}
