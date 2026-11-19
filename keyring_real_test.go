package main

// Pruebas contra el llavero REAL del sistema (macOS Keychain, Windows
// Credential Manager). Solo se ejecutan con LDAPNAV_TEST_REAL_KEYRING=1 y en un
// proceso aparte de los demás tests (los demás usan keyring.MockInit, que no se
// puede deshacer):
//
//	LDAPNAV_TEST_REAL_KEYRING=1 go test -run TestRealKeyring ./...
//
// Usan cuentas únicas bajo el servicio "ldapnav" y las borran al terminar.

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
		t.Fatal("el llavero ya está sustituido por el mock: ejecuta estos tests con -run TestRealKeyring")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	p := Profile{
		URL:    "ldaps://ldap.ejemplo.test:636",
		BindDN: "cn=ldapnav-ci-" + hex.EncodeToString(b) + ",ou=Ñandú,dc=example,dc=org",
	}
	t.Cleanup(func() { _ = keyring.Delete(keychainService, keychainAccount(p)) })
	return p
}

var realKeyringPasswords = []string{
	"simple",
	"con espacios y 'comillas' \"dobles\" $var `cmd` \\ ;&|",
	"ñandú-ü-€-日本語",
	"=:,@/#%",
	"línea1\nlínea2",
}

func TestRealKeyringRoundTrip(t *testing.T) {
	p := realKeyringProfile(t)
	if _, err := keychainGet(p); err == nil {
		t.Fatal("la cuenta no debería existir todavía")
	}
	for _, pw := range realKeyringPasswords {
		if err := keychainSet(p, pw); err != nil {
			t.Fatalf("set %q: %v", pw, err)
		}
		got, err := keychainGet(p)
		if err != nil || got != pw {
			t.Fatalf("get: esperaba %q, obtuve %q (%v)", pw, got, err)
		}
	}
}

// En macOS, una contraseña guardada como lo hacía la versión anterior
// (security add-generic-password -w, texto plano) debe seguir leyéndose.
func TestRealKeyringLegacyMacOSEntries(t *testing.T) {
	p := realKeyringProfile(t)
	if runtime.GOOS != "darwin" {
		t.Skip("solo macOS")
	}
	for _, pw := range realKeyringPasswords[:4] { // sin saltos de línea: security -w los trataría aparte
		out, err := exec.Command("/usr/bin/security", "add-generic-password", "-U",
			"-s", keychainService, "-a", keychainAccount(p), "-w", pw).CombinedOutput()
		if err != nil {
			t.Fatalf("security add: %v\n%s", err, out)
		}
		got, err := keychainGet(p)
		if err != nil || got != pw {
			t.Errorf("entrada antigua %q: obtuve %q (%v)", pw, got, err)
		}
	}
	// y keychainSet sobre una entrada antigua la actualiza (no falla por duplicada)
	if err := keychainSet(p, "nueva"); err != nil {
		t.Fatalf("set sobre entrada antigua: %v", err)
	}
	if got, _ := keychainGet(p); got != "nueva" {
		t.Errorf("tras actualizar: %q", got)
	}
}
