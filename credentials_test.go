package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"ldap-explorer/internal/slapdtest"
)

const vaultPass = "correct horse battery"

type codeErr struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func createVault(t *testing.T, srv *httptest.Server) {
	t.Helper()
	if code := call(t, srv, "POST", "/api/vault/create", map[string]string{"passphrase": vaultPass}, &credStatus{}); code != 200 {
		t.Fatalf("create vault: %d", code)
	}
}

// saveProfile returns the status, and the error body when it was not a success.
func saveProfile(t *testing.T, srv *httptest.Server, p Profile, password string) (int, codeErr) {
	t.Helper()
	resp, body := rawCall(t, srv, "POST", "/api/profiles", map[string]any{"profile": p, "password": password})
	var e codeErr
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal([]byte(body), &e)
	}
	return resp.StatusCode, e
}

func rawCall(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("X-Token", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func profileNames(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	var ps []Profile
	call(t, srv, "GET", "/api/profiles", nil, &ps)
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return names
}

// ---------- status and the vault endpoints ----------

func TestCredentialStatus(t *testing.T) {
	srv := newTestServer(t)
	var st credStatus
	if code := call(t, srv, "GET", "/api/credentials/status", nil, &st); code != 200 {
		t.Fatal(code)
	}
	if !st.Keychain.Available || st.Vault.Exists || st.Vault.Unlocked || st.Vault.IdleTimeoutSeconds != 15*60 {
		t.Errorf("fresh state: %+v", st)
	}
	createVault(t, srv)
	call(t, srv, "GET", "/api/credentials/status", nil, &st)
	if !st.Vault.Exists || !st.Vault.Unlocked {
		t.Errorf("after create: %+v", st)
	}
	call(t, srv, "POST", "/api/vault/lock", nil, &st)
	if !st.Vault.Exists || st.Vault.Unlocked {
		t.Errorf("after lock: %+v", st)
	}

	// A machine without a usable keychain (Linux without Secret Service, WSL...).
	keyring.MockInitWithError(errors.New("no secret service available"))
	defer keyring.MockInit()
	call(t, srv, "GET", "/api/credentials/status", nil, &st)
	if st.Keychain.Available || !strings.Contains(st.Keychain.Reason, "no secret service") {
		t.Errorf("an unusable keychain must be reported with the reason: %+v", st.Keychain)
	}
}

func TestVaultEndpoints(t *testing.T) {
	t.Run("create rules", func(t *testing.T) {
		srv := newTestServer(t)
		var e codeErr
		if code := call(t, srv, "POST", "/api/vault/create", map[string]string{"passphrase": "short"}, &e); code != 400 || e.Code != "weak_passphrase" {
			t.Errorf("weak: %d %+v", code, e)
		}
		createVault(t, srv)
		if code := call(t, srv, "POST", "/api/vault/create", map[string]string{"passphrase": "another passphrase"}, &e); code != 400 || e.Code != "vault_exists" {
			t.Errorf("twice: %d %+v", code, e)
		}
	})
	t.Run("unlock", func(t *testing.T) {
		srv := newTestServer(t)
		var e codeErr
		if code := call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &e); code != 400 || e.Code != "vault_missing" {
			t.Errorf("no vault yet: %d %+v", code, e)
		}
		createVault(t, srv)
		call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
		if code := call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": "not the passphrase"}, &e); code != 400 || e.Code != "wrong_passphrase" {
			t.Errorf("wrong: %d %+v", code, e)
		}
		var st credStatus
		if code := call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &st); code != 200 || !st.Vault.Unlocked {
			t.Errorf("right: %d %+v", code, st)
		}
	})
	t.Run("repeated failures are throttled", func(t *testing.T) {
		srv := newTestServer(t)
		createVault(t, srv)
		call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
		var e codeErr
		for i := 0; i < 4; i++ {
			if code := call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": "wrong wrong wrong"}, &e); code != 400 || e.Code != "wrong_passphrase" {
				t.Fatalf("attempt %d: %d %+v", i+1, code, e)
			}
		}
		resp, body := rawCall(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}) // even the right one
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || !strings.Contains(body, `"throttled"`) {
			t.Errorf("throttling: %d retry-after=%q %s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
		}
	})
	t.Run("change the passphrase", func(t *testing.T) {
		srv := newTestServer(t)
		createVault(t, srv)
		var e codeErr
		if code := call(t, srv, "POST", "/api/vault/passphrase", map[string]string{"oldPassphrase": "nope nope nope", "newPassphrase": "a brand new passphrase"}, &e); code != 400 || e.Code != "wrong_passphrase" {
			t.Errorf("wrong old: %d %+v", code, e)
		}
		if code := call(t, srv, "POST", "/api/vault/passphrase", map[string]string{"oldPassphrase": vaultPass, "newPassphrase": "weak"}, &e); code != 400 || e.Code != "weak_passphrase" {
			t.Errorf("weak new: %d %+v", code, e)
		}
		if code := call(t, srv, "POST", "/api/vault/passphrase", map[string]string{"oldPassphrase": vaultPass, "newPassphrase": "a brand new passphrase"}, &credStatus{}); code != 200 {
			t.Fatalf("change: %d", code)
		}
		call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
		if code := call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": "a brand new passphrase"}, &credStatus{}); code != 200 {
			t.Errorf("the new passphrase: %d", code)
		}
	})
}

// ---------- a profile whose password lives in the vault ----------

func TestProfileWithVaultStorage(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)

	p := Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}
	if code, e := saveProfile(t, srv, p, slapdtest.AlicePW); code != 200 {
		t.Fatalf("save: %d %+v", code, e)
	}
	// The profile says where, never the password.
	profiles, _ := os.ReadFile(profilesPath())
	if bytes.Contains(profiles, []byte(slapdtest.AlicePW)) || !bytes.Contains(profiles, []byte(`"storage": "vault"`)) {
		t.Errorf("profiles.json: %s", profiles)
	}
	// The vault file reveals neither the password nor who it is for.
	raw, err := os.ReadFile(vaultFilePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{slapdtest.AlicePW, slapdtest.AliceDN, l.URL, "alice"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("vault.json contains %q", leak)
		}
	}
	if pw, err := vaultFile().Get(keychainAccount(p)); err != nil || pw != slapdtest.AlicePW {
		t.Fatalf("the vault should hold the password: %q %v", pw, err)
	}

	// Connect without typing it.
	connectOK(t, srv, p, "")

	// Locked: the server asks for the vault to be unlocked; a typed password still works.
	call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
	var e codeErr
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, ""}, &e); code != http.StatusLocked || e.Code != "vault_locked" {
		t.Errorf("locked: %d %+v", code, e)
	}
	connectOK(t, srv, p, slapdtest.AlicePW)
	call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &credStatus{})
	connectOK(t, srv, p, "")

	// A wrong stored password fails like any other wrong password.
	if code, _ := saveProfile(t, srv, p, "stored-wrong-password"); code != 200 {
		t.Fatal(code)
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, ""}, &e); code != 400 || !strings.Contains(e.Error, "bind") {
		t.Errorf("wrong stored password: %d %+v", code, e)
	}
	// and the profile can still be saved without retyping the password.
	if code, _ := saveProfile(t, srv, Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault, BaseDN: "dc=example,dc=org"}, ""); code != 200 {
		t.Fatal(code)
	}
	if pw, _ := vaultFile().Get(keychainAccount(p)); pw != "stored-wrong-password" {
		t.Errorf("saving without a password must keep the stored one: %q", pw)
	}
}

func vaultFilePath() string { return configDir() + string(os.PathSeparator) + "vault.json" }

// ---------- several users and several servers ----------

func TestSeveralUsersAndServers(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)

	profiles := []struct {
		p  Profile
		pw string
	}{
		{Profile{Name: "alice@lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}, slapdtest.AlicePW},
		{Profile{Name: "bob@lab", URL: l.URL, BindDN: slapdtest.BobDN, Storage: storeVault}, slapdtest.BobPW},
		{Profile{Name: "alice@lab-tls", URL: l.LDAPSURL, SkipVerify: true, BindDN: slapdtest.AliceDN, Storage: storeKeychain}, slapdtest.AlicePW},
		{Profile{Name: "admin", URL: l.URL, BindDN: slapdtest.AdminDN, Storage: storeVault}, slapdtest.AdminPW},
	}
	for _, x := range profiles {
		if code, e := saveProfile(t, srv, x.p, x.pw); code != 200 {
			t.Fatalf("save %s: %d %+v", x.p.Name, code, e)
		}
	}

	// Who is connected? Alice has the server's size limit, Bob and the admin do not.
	bulk := "ou=bulk," + slapdtest.BaseDN
	warned := func() bool {
		var r struct {
			Children []struct{} `json:"children"`
			Warning  string     `json:"warning"`
		}
		call(t, srv, "GET", "/api/children?dn="+bulk, nil, &r)
		return r.Warning != ""
	}
	for _, c := range []struct {
		name        string
		limited     bool
		storedWhere string
	}{{"alice@lab", true, storeVault}, {"bob@lab", false, storeVault}, {"alice@lab-tls", true, storeKeychain}, {"admin", false, storeVault}} {
		var p Profile
		for _, x := range profiles {
			if x.p.Name == c.name {
				p = x.p
			}
		}
		connectOK(t, srv, p, "") // every identity connects with its own stored password
		if warned() != c.limited {
			t.Errorf("%s: connected as the wrong user (size-limit warning=%v, want %v)", c.name, warned(), c.limited)
		}
	}

	var list credList
	call(t, srv, "GET", "/api/credentials", nil, &list)
	if len(list.Entries) != 4 {
		t.Fatalf("credentials: %+v", list)
	}
	for _, e := range list.Entries {
		if e.State != "saved" {
			t.Errorf("%s: state %q", e.Profile, e.State)
		}
	}

	// Forget Bob's password: he stops connecting on his own, nobody else is affected.
	call(t, srv, "DELETE", "/api/credentials?profile=bob@lab", nil, &list)
	var bob Profile
	for _, x := range profiles {
		if x.p.Name == "bob@lab" {
			bob = x.p
		}
	}
	var ps []Profile
	call(t, srv, "GET", "/api/profiles", nil, &ps)
	for _, p := range ps {
		if p.Name == "bob@lab" {
			bob = p
			if p.Storage != storeNone {
				t.Errorf("after forgetting, the profile should not save passwords: %+v", p)
			}
		}
	}
	var e codeErr
	if code := call(t, srv, "POST", "/api/connect", connectReq{bob, ""}, &e); code != 400 || !strings.Contains(strings.ToLower(e.Error), "empty password") {
		t.Errorf("bob after forgetting: %d %+v", code, e)
	}
	if ok, _ := vaultFile().Has(keychainAccount(bob)); ok {
		t.Error("Bob's secret is still in the vault")
	}
	connectOK(t, srv, profiles[0].p, "")

	// Deleting a profile forgets its password too.
	call(t, srv, "DELETE", "/api/profiles?name=admin", nil, &ps)
	if ok, _ := vaultFile().Has(keychainAccount(profiles[3].p)); ok {
		t.Error("the deleted profile's secret is still in the vault")
	}
	if ok, _ := vaultFile().Has(keychainAccount(profiles[0].p)); !ok {
		t.Error("deleting another profile removed alice's secret")
	}
}

// The same user on two servers (or the same server over two URLs) has two separate secrets.
func TestSameUserOnTwoServersHasSeparateSecrets(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)
	plain := Profile{Name: "plain", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}
	tls := Profile{Name: "tls", URL: l.LDAPSURL, SkipVerify: true, BindDN: slapdtest.AliceDN, Storage: storeVault}
	saveProfile(t, srv, plain, slapdtest.AlicePW)
	saveProfile(t, srv, tls, "the wrong password for this one")

	connectOK(t, srv, plain, "")
	var e codeErr
	if code := call(t, srv, "POST", "/api/connect", connectReq{tls, ""}, &e); code != 400 || !strings.Contains(e.Error, "bind") {
		t.Errorf("the secret of one server must not be used for the other: %d %+v", code, e)
	}
}

// ---------- moving a password between stores ----------

func TestChangingWhereThePasswordLives(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)
	p := Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeKeychain}
	saveProfile(t, srv, p, slapdtest.AlicePW)
	acct := keychainAccount(p)
	if v, err := keyring.Get(keychainService, acct); err != nil || v != slapdtest.AlicePW {
		t.Fatalf("keychain: %q %v", v, err)
	}

	// keychain -> vault WITHOUT retyping: the password moves, the old copy is removed.
	p.Storage = storeVault
	if code, e := saveProfile(t, srv, p, ""); code != 200 {
		t.Fatalf("move: %d %+v", code, e)
	}
	if _, err := keyring.Get(keychainService, acct); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("the keychain copy must be removed: %v", err)
	}
	if v, err := vaultFile().Get(acct); err != nil || v != slapdtest.AlicePW {
		t.Errorf("the vault must have the password: %q %v", v, err)
	}
	connectOK(t, srv, p, "")

	// vault -> keychain while the vault is locked: it cannot be read, so nothing is changed.
	call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
	p.Storage = storeKeychain
	if code, e := saveProfile(t, srv, p, ""); code != http.StatusLocked || e.Code != "vault_locked" {
		t.Errorf("moving out of a locked vault: %d %+v", code, e)
	}
	var ps []Profile
	call(t, srv, "GET", "/api/profiles", nil, &ps)
	if ps[0].Storage != storeVault {
		t.Errorf("a refused move must not change the profile: %+v", ps[0])
	}
	call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &credStatus{})
	if code, _ := saveProfile(t, srv, p, ""); code != 200 {
		t.Fatal(code)
	}
	if v, _ := keyring.Get(keychainService, acct); v != slapdtest.AlicePW {
		t.Errorf("vault -> keychain: %q", v)
	}
	if ok, _ := vaultFile().Has(acct); ok {
		t.Error("the vault copy must be removed")
	}

	// -> "do not save": the stored copy is forgotten.
	p.Storage = storeNone
	saveProfile(t, srv, p, "")
	if _, err := keyring.Get(keychainService, acct); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("choosing not to save must forget it: %v", err)
	}
}

func TestChangingTheUserForgetsTheOldPassword(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)
	p := Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}
	saveProfile(t, srv, p, slapdtest.AlicePW)
	old := keychainAccount(p)
	p.BindDN = slapdtest.BobDN // the same profile now means another user
	saveProfile(t, srv, p, slapdtest.BobPW)
	if ok, _ := vaultFile().Has(old); ok {
		t.Error("alice's password must not stay behind when the profile now belongs to bob")
	}
	connectOK(t, srv, p, "")
}

// ---------- profiles written by earlier versions ----------

func TestProfileFromBeforeTheVault(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	old := Profile{URL: l.URL, BindDN: slapdtest.AliceDN}
	if err := keyring.Set(keychainService, keychainAccount(old), slapdtest.AlicePW); err != nil {
		t.Fatal(err)
	}
	// What the previous version wrote: useKeychain, no storage field.
	content := `[{"name":"old","url":"` + l.URL + `","startTLS":false,"skipVerify":false,"bindDN":"` + slapdtest.AliceDN + `","baseDN":"","useKeychain":true}]`
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilesPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var ps []Profile
	call(t, srv, "GET", "/api/profiles", nil, &ps)
	if len(ps) != 1 || ps[0].Storage != storeKeychain {
		t.Fatalf("an old profile is read as keychain storage: %+v", ps)
	}
	connectOK(t, srv, ps[0], "")
	// Connecting with only the old flag (a client that never heard of storage) works too.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: slapdtest.AliceDN, UseKeychain: true}, "")
	// Saving it back keeps the flag for the old versions.
	if code, _ := saveProfile(t, srv, ps[0], ""); code != 200 {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(profilesPath())
	if !bytes.Contains(b, []byte(`"storage": "keychain"`)) || !bytes.Contains(b, []byte(`"useKeychain": true`)) {
		t.Errorf("profiles.json: %s", b)
	}
}

// ---------- orphans and the list ----------

func TestCredentialListAndOrphans(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	p := Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}

	// No vault yet.
	saveProfileNoCheck(t, srv, p)
	var list credList
	call(t, srv, "GET", "/api/credentials", nil, &list)
	if list.Entries[0].State != "missing" {
		t.Errorf("no vault: %+v", list.Entries[0])
	}
	createVault(t, srv)
	call(t, srv, "GET", "/api/credentials", nil, &list)
	if list.Entries[0].State != "missing" {
		t.Errorf("vault without the secret: %+v", list.Entries[0])
	}
	saveProfile(t, srv, p, slapdtest.AlicePW)
	call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
	call(t, srv, "GET", "/api/credentials", nil, &list)
	if list.Entries[0].State != "locked" || len(list.Orphans) != 0 {
		t.Errorf("locked: %+v", list)
	}

	// Delete the profile while the vault is locked: its entry cannot be removed and becomes an orphan.
	call(t, srv, "DELETE", "/api/profiles?name=lab", nil, &[]Profile{})
	call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &credStatus{})
	call(t, srv, "GET", "/api/credentials", nil, &list)
	if len(list.Entries) != 0 || len(list.Orphans) != 1 || list.Orphans[0] != keychainAccount(p) {
		t.Fatalf("orphans: %+v", list)
	}
	// It can be removed, and only while unlocked.
	call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
	var e codeErr
	if code := call(t, srv, "DELETE", "/api/credentials?account="+urlQuery(keychainAccount(p)), nil, &e); code != http.StatusLocked {
		t.Errorf("locked: %d %+v", code, e)
	}
	call(t, srv, "POST", "/api/vault/unlock", map[string]string{"passphrase": vaultPass}, &credStatus{})
	call(t, srv, "DELETE", "/api/credentials?account="+urlQuery(keychainAccount(p)), nil, &list)
	if len(list.Orphans) != 0 {
		t.Errorf("after removing the orphan: %+v", list)
	}

	// An entry a profile still uses is not an orphan and cannot be removed this way.
	saveProfile(t, srv, p, slapdtest.AlicePW)
	if code := call(t, srv, "DELETE", "/api/credentials?account="+urlQuery(keychainAccount(p)), nil, &e); code != 400 || !strings.Contains(e.Error, "still uses") {
		t.Errorf("deleting a used entry: %d %+v", code, e)
	}
	if code := call(t, srv, "DELETE", "/api/credentials?profile=nobody", nil, &e); code != 400 {
		t.Errorf("an unknown profile: %d", code)
	}
}

func saveProfileNoCheck(t *testing.T, srv *httptest.Server, p Profile) {
	t.Helper()
	// no password: nothing to check or store, only the choice is recorded
	if code, e := saveProfile(t, srv, p, ""); code != 200 {
		t.Fatalf("save: %d %+v", code, e)
	}
}

func urlQuery(s string) string {
	return strings.NewReplacer("=", "%3D", ",", "%2C", "@", "%40", ":", "%3A", "/", "%2F").Replace(s)
}

// ---------- refusing before changing anything ----------

func TestSavingIsRefusedBeforeAnythingChanges(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	p := Profile{Name: "lab", URL: l.URL, BindDN: slapdtest.AliceDN}

	// vault storage, no vault yet
	p.Storage = storeVault
	if code, e := saveProfile(t, srv, p, "pw"); code != 400 || e.Code != "vault_missing" {
		t.Errorf("no vault: %d %+v", code, e)
	}
	// locked vault
	createVault(t, srv)
	call(t, srv, "POST", "/api/vault/lock", nil, &credStatus{})
	if code, e := saveProfile(t, srv, p, "pw"); code != http.StatusLocked || e.Code != "vault_locked" {
		t.Errorf("locked: %d %+v", code, e)
	}
	// keychain that cannot be used
	keyring.MockInitWithError(errors.New("no secret service available"))
	p.Storage = storeKeychain
	if code, e := saveProfile(t, srv, p, "pw"); code != 400 || e.Code != "keychain_unavailable" || !strings.Contains(e.Error, "no secret service") {
		t.Errorf("keychain: %d %+v", code, e)
	}
	keyring.MockInit()
	// unknown storage
	p.Storage = "carrier-pigeon"
	if code, _ := saveProfile(t, srv, p, "pw"); code != 400 {
		t.Errorf("unknown storage: %d", code)
	}
	if names := profileNames(t, srv); len(names) != 0 {
		t.Errorf("a refused save must not create the profile: %v", names)
	}

	// Without a password there is nothing to store, so a locked vault does not block editing.
	p.Storage = storeVault
	if code, e := saveProfile(t, srv, p, ""); code != 200 {
		t.Errorf("saving without a password while locked: %d %+v", code, e)
	}
}

// ---------- the API never hands out secrets ----------

func TestSecretsNeverLeaveTheServer(t *testing.T) {
	l := slapdtest.Start(t)
	srv := newTestServer(t)
	createVault(t, srv)
	const secret = "TOP-SECRET-password-123"
	saveProfile(t, srv, Profile{Name: "a", URL: l.URL, BindDN: slapdtest.AliceDN, Storage: storeVault}, secret)
	saveProfile(t, srv, Profile{Name: "b", URL: l.URL, BindDN: slapdtest.BobDN, Storage: storeKeychain}, secret)

	for _, path := range []string{"/api/profiles", "/api/credentials", "/api/credentials/status", "/api/queries"} {
		resp, body := rawCall(t, srv, "GET", path, nil)
		if strings.Contains(body, secret) {
			t.Errorf("GET %s returned the password", path)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s may be cached: %q", path, resp.Header.Get("Cache-Control"))
		}
	}
	for _, p := range []struct{ method, path string }{
		{"POST", "/api/vault/lock"}, {"POST", "/api/vault/unlock"}, {"POST", "/api/vault/create"}, {"POST", "/api/vault/passphrase"},
	} {
		_, body := rawCall(t, srv, p.method, p.path, map[string]string{"passphrase": vaultPass, "oldPassphrase": vaultPass, "newPassphrase": vaultPass + "!"})
		if strings.Contains(body, secret) || strings.Contains(body, vaultPass) {
			t.Errorf("%s %s echoed a secret: %s", p.method, p.path, body)
		}
	}
	// a failed connect must not echo the password either
	_, body := rawCall(t, srv, "POST", "/api/connect", connectReq{Profile{URL: "ldap://127.0.0.1:1", BindDN: slapdtest.AliceDN}, secret})
	if strings.Contains(body, secret) {
		t.Errorf("a failed connection echoed the password: %s", body)
	}
}
