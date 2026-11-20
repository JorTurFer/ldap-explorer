package main

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ldap-explorer/internal/ldif"
	"ldap-explorer/internal/slapdtest"
)

func decodeLDIF(t *testing.T, text string) []ldif.Entry {
	t.Helper()
	es, err := ldif.Decode(strings.NewReader(text))
	if err != nil {
		t.Fatalf("what we export must be valid LDIF: %v\n%s", err, text)
	}
	return es
}

func attrValues(e ldif.Entry, name string) [][]byte {
	for _, a := range e.Attrs {
		if strings.EqualFold(a.Name, name) {
			return a.Values
		}
	}
	return nil
}

func TestExportEntryAsLDIF(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	get := func(dn string, extra string) (*http.Response, string) {
		return rawCall(t, srv, "GET", "/api/entry/ldif?dn="+url.QueryEscape(dn)+extra, nil)
	}

	resp, body := get(slapdtest.AliceDN, "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("%d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="alice.ldif"` {
		t.Errorf("Content-Disposition: %q", cd)
	}
	es := decodeLDIF(t, body)
	if len(es) != 1 || es[0].DN != slapdtest.AliceDN {
		t.Fatalf("entries: %+v", es)
	}
	if es[0].Attrs[0].Name != "objectClass" {
		t.Errorf("objectClass first, like the entry view: %s", es[0].Attrs[0].Name)
	}
	if v := attrValues(es[0], "mail"); len(v) != 1 || string(v[0]) != "alice@example.org" {
		t.Errorf("mail: %q", v)
	}
	if attrValues(es[0], "entryUUID") != nil {
		t.Error("operational attributes only with operational=1")
	}

	// A binary value comes out byte for byte: compare with what the seed file says.
	seed, err := os.ReadFile("internal/slapdtest/seed.ldif")
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, e := range decodeLDIF(t, string(seed)) {
		if e.DN == slapdtest.AliceDN {
			want = attrValues(e, "jpegPhoto")[0]
		}
	}
	got := attrValues(es[0], "jpegPhoto")
	if len(want) == 0 || len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Errorf("the photo is not identical after a round trip through the server (%d bytes, want %d)", len(got[0]), len(want))
	}
	if !bytes.HasPrefix(want, []byte{0xff, 0xd8}) {
		t.Error("the seed photo should be a JPEG")
	}

	// Operational attributes on request.
	_, body = get(slapdtest.AliceDN, "&operational=1")
	if attrValues(decodeLDIF(t, body)[0], "entryUUID") == nil {
		t.Error("operational=1 should include entryUUID")
	}

	// Non-ASCII text is base64, and reads back.
	zoe := "uid=zoe,ou=people," + slapdtest.BaseDN
	_, body = get(zoe, "")
	if strings.Contains(body, "Zoë") || strings.Contains(body, "Ñandú") {
		t.Error("non-ASCII text must be base64 in LDIF")
	}
	if v := attrValues(decodeLDIF(t, body)[0], "cn"); len(v) != 1 || string(v[0]) != "Zoë Ñandú" {
		t.Errorf("cn after a round trip: %q", v)
	}
	// Several values of one attribute.
	_, body = get("uid=bob,ou=people,"+slapdtest.BaseDN, "")
	if v := attrValues(decodeLDIF(t, body)[0], "telephoneNumber"); len(v) != 2 {
		t.Errorf("both phone numbers: %q", v)
	}
	// A DN full of characters that need care.
	weird := `cn=Weird (Name) *star* \5Cback,ou=special,` + slapdtest.BaseDN
	resp, body = get(weird, "")
	if resp.StatusCode != 200 || decodeLDIF(t, body)[0].DN != weird {
		t.Errorf("weird DN: %d %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="[A-Za-z0-9._-]+\.ldif"$`).MatchString(cd) {
		t.Errorf("the file name must be safe: %q", cd)
	}
}

func TestExportErrors(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	if resp, _ := rawCall(t, srv, "GET", "/api/entry/ldif?dn=x", nil); resp.StatusCode != 400 {
		t.Errorf("not connected: %d", resp.StatusCode)
	}
	if resp, _ := rawCall(t, srv, "POST", "/api/search/ldif", map[string]string{"base": "x"}); resp.StatusCode != 400 {
		t.Errorf("not connected: %d", resp.StatusCode)
	}
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"no dn", "GET", "/api/entry/ldif", nil},
		{"missing entry", "GET", "/api/entry/ldif?dn=" + url.QueryEscape("uid=nobody,"+slapdtest.BaseDN), nil},
		{"invalid filter", "POST", "/api/search/ldif", map[string]string{"base": slapdtest.BaseDN, "filter": "(uid="}},
		{"missing base", "POST", "/api/search/ldif", map[string]string{"base": "ou=nothing," + slapdtest.BaseDN}},
	} {
		if resp, body := rawCall(t, srv, tc.method, tc.path, tc.body); resp.StatusCode != 400 || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s", tc.name, resp.StatusCode, body)
		}
	}
}

func TestExportSearchAsLDIF(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)
	people := "ou=people," + slapdtest.BaseDN

	resp, body := rawCall(t, srv, "POST", "/api/search/ldif", map[string]any{"base": people, "scope": "one", "filter": "(objectClass=inetOrgPerson)", "attrs": "uid mail"})
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="ldap-explorer-\d{4}-\d{2}-\d{2}-\d{6}\.ldif"$`).MatchString(cd) {
		t.Errorf("file name: %q", cd)
	}
	es := decodeLDIF(t, body)
	var dns []string
	for _, e := range es {
		dns = append(dns, strings.SplitN(e.DN, ",", 2)[0])
		for _, a := range e.Attrs {
			if n := strings.ToLower(a.Name); n != "uid" && n != "mail" {
				t.Errorf("%s: only the requested attributes: got %s", e.DN, a.Name)
			}
		}
	}
	slices.Sort(dns)
	if !slices.Equal(dns, []string{"uid=alice", "uid=bob", "uid=zoe"}) {
		t.Errorf("entries: %v", dns)
	}
	if resp.Header.Get("X-Warning") != "" {
		t.Errorf("no warning expected: %q", resp.Header.Get("X-Warning"))
	}

	// Binary attributes survive a search export too.
	_, body = rawCall(t, srv, "POST", "/api/search/ldif", map[string]any{"base": people, "scope": "one", "filter": "(uid=alice)", "attrs": "jpegPhoto"})
	if v := attrValues(decodeLDIF(t, body)[0], "jpegPhoto"); len(v) != 1 || !bytes.HasPrefix(v[0], []byte{0xff, 0xd8}) {
		t.Errorf("jpegPhoto in a search export: %q", v)
	}

	// The server's size limit makes the export partial, and it says so.
	resp, body = rawCall(t, srv, "POST", "/api/search/ldif", map[string]any{"base": "ou=bulk," + slapdtest.BaseDN, "scope": "one", "limit": 10000, "attrs": "uid"})
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("X-Warning"), "Size Limit") {
		t.Errorf("partial export: %d warning=%q", resp.StatusCode, resp.Header.Get("X-Warning"))
	}
	if n := len(decodeLDIF(t, body)); n == 0 || n >= slapdtest.BulkCount {
		t.Errorf("a partial export has some entries, not all: %d", n)
	}
	// ...while an unrestricted user gets everything.
	connectOK(t, srv, Profile{Name: "bob", URL: l.URL, BindDN: slapdtest.BobDN}, slapdtest.BobPW)
	resp, body = rawCall(t, srv, "POST", "/api/search/ldif", map[string]any{"base": "ou=bulk," + slapdtest.BaseDN, "scope": "one", "limit": 10000, "attrs": "uid"})
	if n := len(decodeLDIF(t, body)); n != slapdtest.BulkCount || resp.Header.Get("X-Warning") != "" {
		t.Errorf("complete export: %d entries, warning %q", n, resp.Header.Get("X-Warning"))
	}
}

func TestLDIFFilename(t *testing.T) {
	for dn, want := range map[string]string{
		"uid=alice,ou=people,dc=x":    "alice.ldif",
		"cn=Zoë Ñandú,dc=x":           "Zo_and.ldif",
		`cn=Weird (Name) *star*,dc=x`: "Weird_Name_star.ldif",
		"dc=example,dc=org":           "example.ldif",
		"cn=../../etc/passwd,dc=x":    "etc_passwd.ldif",
		"":                            "entry.ldif",
		"cn=,dc=x":                    "entry.ldif",
	} {
		if got := ldifFilename(dn); got != want {
			t.Errorf("ldifFilename(%q) = %q, want %q", dn, got, want)
		}
	}
}
