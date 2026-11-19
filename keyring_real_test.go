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
	"os"
	"os/exec"
	"runtime"
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
func TestRealKeyringLegacyMacOSEntries(t *testing.T) {
	p := realKeyringProfile(t)
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	for _, pw := range realKeyringPasswords[:4] { // no newlines: security -w would treat them separately
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
