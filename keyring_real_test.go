package main

// Tests against the system's REAL keychain (macOS Keychain, Windows
// Credential Manager). They only run with LDAPNAV_TEST_REAL_KEYRING=1 and in a
// separate process from the other tests (the others use keyring.MockInit, which
// cannot be undone):
//
//	LDAPNAV_TEST_REAL_KEYRING=1 go test -run TestRealKeyring ./...
//
// They use unique accounts under the "ldapnav" service and delete them when done.

import (
	"crypto/rand"
	"encoding/hex"
	"ldapnav/internal/slapdtest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func realKeyringProfile(t *testing.T) Profile {
	t.Helper()
	if os.Getenv("LDAPNAV_TEST_REAL_KEYRING") != "1" {
		t.Skip("LDAPNAV_TEST_REAL_KEYRING != 1")
	}
	if keyringMocked {
		t.Fatal("the keychain has already been replaced by the mock: run these tests with -run TestRealKeyring")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	p := Profile{
		URL:    "ldaps://ldap.example.test:636",
		BindDN: "cn=ldapnav-ci-" + hex.EncodeToString(b) + ",ou=Ñandú,dc=example,dc=org",
	}
	t.Cleanup(func() { _ = keyring.Delete(keychainService, keychainAccount(p)) })
	return p
}

var realKeyringPasswords = []string{
	"simple",
	"with spaces and 'comillas' \"dobles\" $var `cmd` \\ ;&|",
	"ñandú-ü-€-日本語",
	"=:,@/#%",
	"line1\nline2",
}

func TestRealKeyringRoundTrip(t *testing.T) {
	p := realKeyringProfile(t)
	if _, err := keychainGet(p); err == nil {
		t.Fatal("the account should not exist yet")
	}
	for _, pw := range realKeyringPasswords {
		if err := keychainSet(p, pw); err != nil {
			t.Fatalf("set %q: %v", pw, err)
		}
		got, err := keychainGet(p)
		if err != nil || got != pw {
			t.Fatalf("get: expected %q, got %q (%v)", pw, got, err)
		}
	}
}

// On macOS, a password stored the way the previous version did it
// (security add-generic-password -w, plain text) must still be readable.
//
// Known limitation, not a regression: `security find-generic-password -w`
// prints non-ASCII values hex-encoded, and the previous version read entries
// with that same command, so legacy entries with non-ASCII passwords were
// already unusable; saving the profile password again rewrites them in the
// format go-keyring reads back correctly (covered by TestRealKeyringRoundTrip).
func TestRealKeyringLegacyMacOSEntries(t *testing.T) {
	p := realKeyringProfile(t)
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	for _, pw := range realKeyringPasswords {
		if !isASCIISingleLine(pw) {
			continue // see the known limitation above
		}
		out, err := exec.Command("/usr/bin/security", "add-generic-password", "-U",
			"-s", keychainService, "-a", keychainAccount(p), "-w", pw).CombinedOutput()
		if err != nil {
			t.Fatalf("security add: %v\n%s", err, out)
		}
		got, err := keychainGet(p)
		if err != nil || got != pw {
			t.Errorf("legacy entry %q: got %q (%v)", pw, got, err)
		}
	}
	// and keychainSet on a legacy entry updates it (does not fail as a duplicate)
	if err := keychainSet(p, "new"); err != nil {
		t.Fatalf("set on legacy entry: %v", err)
	}
	if got, _ := keychainGet(p); got != "new" {
		t.Errorf("after update: %q", got)
	}
}

func isASCIISingleLine(s string) bool {
	for _, r := range s {
		if r > 0x7e || r < 0x20 {
			return false
		}
	}
	return true
}

// End to end with the real OS keychain and a real OpenLDAP: the password is
// saved with keychainSet, then /api/connect is called WITHOUT a password and
// must bind using the one stored in the keychain. It does not touch
// profiles.json or HOME (the macOS keychain lives under HOME).
func TestRealKeyringEndToEnd(t *testing.T) {
	realKeyringProfile(t) // gate + mock check; the account below is unique too
	l := slapdtest.Start(t)
	// The account is bindDN@url with the real DN and the ephemeral slapd URL
	// (unique per run, random port); it is deleted on cleanup.
	p := Profile{Name: "e2e", URL: l.URL, BindDN: slapdtest.AliceDN, UseKeychain: true}
	t.Cleanup(func() { _ = keyring.Delete(keychainService, keychainAccount(p)) })
	if err := keychainSet(p, slapdtest.AlicePW); err != nil {
		t.Fatal(err)
	}
	srv := serveHandler(t)
	connectOK(t, srv, p, "")

	var e entryResp
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(slapdtest.AliceDN), nil, &e); code != 200 || e.attr("mail") == nil {
		t.Fatalf("entry through the keychain-authenticated session: %d %+v", code, e)
	}

	// A wrong password stored in the keychain must make the bind fail.
	if err := keychainSet(p, "wrong-password"); err != nil {
		t.Fatal(err)
	}
	var er errResp
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, ""}, &er); code != 400 || !strings.Contains(er.Error, "bind") {
		t.Errorf("wrong keychain password: %d %q", code, er.Error)
	}
}
