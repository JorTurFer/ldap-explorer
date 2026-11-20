package main

import (
	"bytes"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/JorTurFer/ldap-explorer/internal/slapdtest"
)

func valueURL(dn, attr string, extra string) string {
	return "/api/entry/value?dn=" + url.QueryEscape(dn) + "&attr=" + url.QueryEscape(attr) + extra
}

func TestAttributeValueBytes(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), slapdtest.AlicePW)

	// The photo, byte for byte, as the seed file defines it.
	seed, _ := os.ReadFile("internal/slapdtest/seed.ldif")
	var want []byte
	for _, e := range decodeLDIF(t, string(seed)) {
		if e.DN == slapdtest.AliceDN {
			want = attrValues(e, "jpegPhoto")[0]
		}
	}
	resp, body := rawCall(t, srv, "GET", valueURL(slapdtest.AliceDN, "jpegPhoto", ""), nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal([]byte(body), want) {
		t.Fatalf("photo: %d %q (%d bytes, want %d)", resp.StatusCode, resp.Header.Get("Content-Type"), len(body), len(want))
	}
	if resp.Header.Get("Content-Disposition") != "" {
		t.Error("a preview is not a download")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	// Case-insensitive attribute name, and download=1 names the file after what it is.
	resp, body2 := rawCall(t, srv, "GET", valueURL(slapdtest.AliceDN, "JPEGPHOTO", "&download=1"), nil)
	if body2 != body || resp.Header.Get("Content-Disposition") != `attachment; filename="jpegPhoto.jpg"` { // the canonical name, not the one typed
		t.Errorf("download: %q", resp.Header.Get("Content-Disposition"))
	}
	// Text values work too, and the index chooses among several.
	bob := "uid=bob,ou=people," + slapdtest.BaseDN
	for i, wantPhone := range []string{"+34 600 000 001", "+34 600 000 002"} {
		resp, body := rawCall(t, srv, "GET", valueURL(bob, "telephoneNumber", "&i="+string(rune('0'+i))), nil)
		if resp.StatusCode != 200 || body != wantPhone || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
			t.Errorf("phone %d: %d %q %q", i, resp.StatusCode, body, resp.Header.Get("Content-Type"))
		}
	}
	// Errors say what is wrong.
	for _, tc := range []struct{ name, path, msg string }{
		{"no dn", "/api/entry/value?attr=mail", "required"},
		{"no attr", "/api/entry/value?dn=x", "required"},
		{"index too high", valueURL(bob, "telephoneNumber", "&i=5"), "no value number 5"},
		{"negative index", valueURL(bob, "mail", "&i=-1"), "non-negative"},
		{"not a number", valueURL(bob, "mail", "&i=x"), "non-negative"},
		{"no such attribute", valueURL(bob, "jpegPhoto", ""), "has no attribute"},
		{"no such entry", valueURL("uid=nobody,"+slapdtest.BaseDN, "mail", ""), "No Such Object"},
	} {
		resp, body := rawCall(t, srv, "GET", tc.path, nil)
		if resp.StatusCode != 400 || !strings.Contains(body, tc.msg) {
			t.Errorf("%s: %d %s", tc.name, resp.StatusCode, body)
		}
	}
}

func TestAttributeValueNeedsAConnection(t *testing.T) {
	srv := newTestServer(t)
	if resp, body := rawCall(t, srv, "GET", valueURL("cn=x", "mail", ""), nil); resp.StatusCode != 400 || !strings.Contains(body, "not connected") {
		t.Errorf("%d %s", resp.StatusCode, body)
	}
}

func TestExtensionFor(t *testing.T) {
	for ct, want := range map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
		"text/plain; charset=utf-8": ".bin", "application/octet-stream": ".bin",
	} {
		if got := extensionFor(ct); got != want {
			t.Errorf("extensionFor(%q) = %q, want %q", ct, got, want)
		}
	}
}
