package vault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var cheap = KDF{Time: 1, MemoryKiB: 8, Threads: 1} // fast for tests; the real cost is DefaultKDF

const pass = "correct horse battery"

func newVault(t *testing.T) (*Vault, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "vault.json")
	v := Open(path)
	v.SetKDF(cheap)
	return v, path
}

func created(t *testing.T) (*Vault, string) {
	t.Helper()
	v, path := newVault(t)
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	return v, path
}

func reopen(path string) *Vault {
	v := Open(path)
	v.SetKDF(cheap)
	return v
}

func TestLifecycle(t *testing.T) {
	v, path := newVault(t)
	if v.Exists() || v.Unlocked() {
		t.Fatal("a new handle has no vault")
	}
	if err := v.Unlock(pass); !errors.Is(err, ErrNoVault) {
		t.Errorf("unlocking a vault that does not exist: %v", err)
	}
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if !v.Exists() || !v.Unlocked() {
		t.Error("Create leaves the vault created and unlocked")
	}
	if err := v.Create(pass); !errors.Is(err, ErrExists) {
		t.Errorf("creating twice: %v", err)
	}

	for acct, secret := range map[string]string{
		"cn=admin,dc=example,dc=org@ldaps://ldap.example.org": "s3cret!",
		"uid=bob,ou=people,dc=x@ldap://127.0.0.1:389":         "p@ss w0rd ñandú €日本",
		"@ldap://anonymous.example.org":                       "",
	} {
		if err := v.Set(acct, secret); err != nil {
			t.Fatal(err)
		}
		got, err := v.Get(acct)
		if err != nil || got != secret {
			t.Errorf("Get(%q) = %q, %v", acct, got, err)
		}
	}
	if _, err := v.Get("unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing account: %v", err)
	}
	if ok, err := v.Has("unknown"); ok || err != nil {
		t.Errorf("Has(unknown) = %v, %v", ok, err)
	}
	accts, err := v.Accounts()
	if err != nil || len(accts) != 3 || !slices.IsSorted(accts) {
		t.Errorf("accounts: %v %v", accts, err)
	}

	// Replace, delete (deleting a missing one is fine).
	if err := v.Set("uid=bob,ou=people,dc=x@ldap://127.0.0.1:389", "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := v.Get("uid=bob,ou=people,dc=x@ldap://127.0.0.1:389"); got != "new" {
		t.Errorf("replaced secret: %q", got)
	}
	if err := v.Delete("uid=bob,ou=people,dc=x@ldap://127.0.0.1:389"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("never existed"); err != nil {
		t.Errorf("deleting a missing account: %v", err)
	}
	if accts, _ := v.Accounts(); len(accts) != 2 {
		t.Errorf("after delete: %v", accts)
	}

	// Everything survives locking and opening the file again.
	v.Lock()
	if v.Unlocked() {
		t.Fatal("Lock must lock")
	}
	w := reopen(path)
	if err := w.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	if got, err := w.Get("cn=admin,dc=example,dc=org@ldaps://ldap.example.org"); err != nil || got != "s3cret!" {
		t.Errorf("after reopening: %q %v", got, err)
	}
}

func TestLockedVaultRefusesEverything(t *testing.T) {
	v, _ := created(t)
	if err := v.Set("a", "b"); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	if _, err := v.Get("a"); !errors.Is(err, ErrLocked) {
		t.Errorf("Get: %v", err)
	}
	if err := v.Set("a", "c"); !errors.Is(err, ErrLocked) {
		t.Errorf("Set: %v", err)
	}
	if err := v.Delete("a"); !errors.Is(err, ErrLocked) {
		t.Errorf("Delete: %v", err)
	}
	if _, err := v.Has("a"); !errors.Is(err, ErrLocked) {
		t.Errorf("Has: %v", err)
	}
	if _, err := v.Accounts(); !errors.Is(err, ErrLocked) {
		t.Errorf("Accounts: %v", err)
	}
}

func TestPassphraseRules(t *testing.T) {
	v, _ := newVault(t)
	for _, weak := range []string{"", "short", "1234567", "ñandú€€"} { // 7 characters at most (counted as characters, not bytes)
		if err := v.Create(weak); !errors.Is(err, ErrWeakPassphrase) {
			t.Errorf("Create(%q): %v", weak, err)
		}
	}
	if v.Exists() {
		t.Error("a rejected passphrase must not create the file")
	}
	if err := v.Create("12345678"); err != nil { // exactly 8 is allowed
		t.Errorf("8 characters: %v", err)
	}
}

func TestWrongPassphrase(t *testing.T) {
	v, path := created(t)
	v.Lock()
	w := reopen(path)
	if err := w.Unlock("not the passphrase"); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("wrong passphrase: %v", err)
	}
	if w.Unlocked() {
		t.Error("a wrong passphrase must not unlock")
	}
	if err := w.Unlock(pass); err != nil {
		t.Errorf("the right one still works: %v", err)
	}
}

// The file must not reveal what it protects: not the secrets, nor which users or
// servers have an entry.
func TestFileRevealsNothing(t *testing.T) {
	v, path := created(t)
	secrets := map[string]string{
		"cn=admin,dc=secret-corp,dc=example@ldaps://ldap.secret-corp.example:636": "Tr0ub4dor&3-very-secret",
		"uid=maria.garcia,ou=ops@ldap://10.20.30.40":                              "another-Secret-Value",
	}
	for a, s := range secrets {
		if err := v.Set(a, s); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for a, s := range secrets {
		for _, leak := range []string{s, a, "secret-corp", "maria.garcia", "10.20.30.40", "ldaps://", "cn=admin", "Tr0ub4dor"} {
			if bytes.Contains(raw, []byte(leak)) {
				t.Errorf("the vault file contains %q", leak)
			}
		}
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Entries) != 2 {
		t.Fatalf("structure: %v (%d entries)", err, len(f.Entries))
	}
	// Encoding is not hiding: the entry identifiers must not decode to the account names either.
	for id := range f.Entries {
		decoded, _ := base64.RawURLEncoding.DecodeString(id)
		for a := range secrets {
			if bytes.Contains(decoded, []byte(a)) || bytes.Contains(decoded, []byte("secret-corp")) || bytes.Contains(decoded, []byte("maria")) {
				t.Errorf("entry identifier %q is an encoding of an account name", id)
			}
		}
		if len(decoded) != 32 {
			t.Errorf("identifiers should be 32-byte HMACs, got %d bytes", len(decoded))
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("permissions: %v", st.Mode().Perm())
		}
		if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
			t.Errorf("folder permissions: %v", st.Mode().Perm())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".vault-*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestNoncesAreFreshEveryTime(t *testing.T) {
	v, path := created(t)
	var cts, nonces []string
	for i := 0; i < 3; i++ {
		if err := v.Set("same", "same secret"); err != nil {
			t.Fatal(err)
		}
		f, err := readFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range f.Entries {
			cts, nonces = append(cts, s.CT), append(nonces, s.Nonce)
		}
	}
	for _, list := range [][]string{cts, nonces} {
		seen := map[string]bool{}
		for _, x := range list {
			if seen[x] {
				t.Errorf("a nonce or ciphertext was reused")
			}
			seen[x] = true
		}
	}
}

func TestTamperingIsDetected(t *testing.T) {
	v, path := created(t)
	for _, a := range []string{"a@x", "b@y"} {
		if err := v.Set(a, "secret-of-"+a); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := readFile(path)
	var ids []string
	for id := range f.Entries {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	write := func(mut func(f *file)) {
		g, _ := readFile(path)
		mut(g)
		b, _ := json.Marshal(g)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// flipping one bit of a ciphertext
	write(func(g *file) {
		e := g.Entries[ids[0]]
		raw, _ := unb64(e.CT)
		raw[0] ^= 1
		e.CT = b64(raw)
		g.Entries[ids[0]] = e
	})
	failed := 0
	for _, a := range []string{"a@x", "b@y"} {
		if _, err := v.Get(a); errors.Is(err, ErrCorrupt) {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("exactly the altered entry must be reported as damaged, %d were", failed)
	}
	if _, err := v.Accounts(); !errors.Is(err, ErrCorrupt) {
		t.Errorf("listing must notice the damage: %v", err)
	}

	// swapping two valid entries between their identifiers
	v2, path2 := created(t)
	_ = v2.Set("a@x", "secret-a")
	_ = v2.Set("b@y", "secret-b")
	g, _ := readFile(path2)
	var keys []string
	for id := range g.Entries {
		keys = append(keys, id)
	}
	g.Entries[keys[0]], g.Entries[keys[1]] = g.Entries[keys[1]], g.Entries[keys[0]]
	b, _ := json.Marshal(g)
	_ = os.WriteFile(path2, b, 0o600)
	for _, a := range []string{"a@x", "b@y"} {
		if got, err := v2.Get(a); err == nil {
			t.Errorf("a swapped entry was accepted for %q (returned %q)", a, got)
		}
	}
}

func TestDamagedFiles(t *testing.T) {
	good := func() []byte {
		_, path := created(t)
		b, _ := os.ReadFile(path)
		return b
	}()
	var base file
	_ = json.Unmarshal(good, &base)

	mod := func(mut func(f *file)) []byte {
		f := base
		mut(&f)
		b, _ := json.Marshal(f)
		return b
	}
	for name, content := range map[string][]byte{
		"not json":            []byte("{nope"),
		"empty":               {},
		"unknown version":     mod(func(f *file) { f.Version = 99 }),
		"unknown kdf":         mod(func(f *file) { f.KDF.Name = "md5" }),
		"zero cost":           mod(func(f *file) { f.KDF.Time = 0 }),
		"gigabytes of memory": mod(func(f *file) { f.KDF.MemoryKiB = 1 << 30 }),
		"endless passes":      mod(func(f *file) { f.KDF.Time = 1 << 30 }),
		"too many threads":    mod(func(f *file) { f.KDF.Threads = 200 }),
		"memory below spec":   mod(func(f *file) { f.KDF.Threads = 4; f.KDF.MemoryKiB = 8 }),
		"broken salt":         mod(func(f *file) { f.KDF.Salt = "!!!" }),
		"broken check":        mod(func(f *file) { f.Check.CT = "AAAA" }),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "vault.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			v := reopen(path)
			start := time.Now()
			err := v.Unlock(pass)
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrWrongPassphrase) {
				t.Errorf("unlock: %v", err)
			}
			if time.Since(start) > 5*time.Second {
				t.Error("a damaged file made unlocking slow")
			}
			if v.Unlocked() {
				t.Error("a damaged file must not unlock")
			}
			// It is never overwritten, not even by Create.
			if err := v.Create(pass); !errors.Is(err, ErrExists) {
				t.Errorf("Create over a damaged file: %v", err)
			}
			if b, _ := os.ReadFile(path); !bytes.Equal(b, content) {
				t.Error("the damaged file was modified")
			}
		})
	}
}

func TestChangePassphrase(t *testing.T) {
	v, path := created(t)
	_ = v.Set("a@x", "secret-a")
	_ = v.Set("b@y", "secret-b")
	before, _ := os.ReadFile(path)
	var fb file
	_ = json.Unmarshal(before, &fb)

	// wrong old passphrase / weak new one: nothing changes
	if err := v.ChangePassphrase("wrong wrong wrong", "a brand new passphrase"); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("wrong old passphrase: %v", err)
	}
	if err := v.ChangePassphrase(pass, "weak"); !errors.Is(err, ErrWeakPassphrase) {
		t.Errorf("weak new passphrase: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Error("a failed change modified the file")
	}

	if err := v.ChangePassphrase(pass, "a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	if !v.Unlocked() {
		t.Error("it stays unlocked after changing the passphrase")
	}
	var fa file
	b, _ := os.ReadFile(path)
	_ = json.Unmarshal(b, &fa)
	if fa.KDF.Salt == fb.KDF.Salt {
		t.Error("the salt must be renewed")
	}
	v.Lock()
	w := reopen(path)
	if err := w.Unlock(pass); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("the old passphrase must stop working: %v", err)
	}
	if err := w.Unlock("a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	for acct, want := range map[string]string{"a@x": "secret-a", "b@y": "secret-b"} {
		if got, err := w.Get(acct); err != nil || got != want {
			t.Errorf("%s after re-keying: %q %v", acct, got, err)
		}
	}
}

func TestFailedUnlocksAreThrottled(t *testing.T) {
	v, path := created(t)
	v.Lock()
	w := reopen(path)
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }

	for i := 0; i < 3; i++ { // the first three attempts are free
		if err := w.Unlock("nope nope nope"); !errors.Is(err, ErrWrongPassphrase) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	err := w.Unlock("nope nope nope")
	var tm *TooManyAttemptsError
	if !errors.As(err, &tm) && !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("4th attempt: %v", err)
	}
	// the 4th real attempt is allowed and fails; then the next one is blocked
	if errors.Is(err, ErrWrongPassphrase) {
		err = w.Unlock(pass) // even the right passphrase is refused while throttled
	}
	if !errors.As(err, &tm) {
		t.Fatalf("expected throttling, got %v", err)
	}
	if tm.RetryAfter <= 0 || tm.RetryAfter > time.Minute {
		t.Errorf("retry after %v", tm.RetryAfter)
	}
	if w.Unlocked() {
		t.Error("throttling must not unlock")
	}
	// after waiting, the right passphrase works and the counter resets
	clock = clock.Add(2 * time.Minute)
	if err := w.Unlock(pass); err != nil {
		t.Fatalf("after waiting: %v", err)
	}
	w.Lock()
	for i := 0; i < 3; i++ {
		if err := w.Unlock("nope nope nope"); !errors.Is(err, ErrWrongPassphrase) {
			t.Errorf("the counter should have been reset (attempt %d): %v", i+1, err)
		}
	}
}

func TestDelayGrowsButIsCapped(t *testing.T) {
	v, path := created(t)
	v.Lock()
	w := reopen(path)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }
	var delays []time.Duration
	for i := 0; i < 14; i++ {
		err := w.Unlock("wrong wrong wrong")
		var tm *TooManyAttemptsError
		if errors.As(err, &tm) {
			t.Fatalf("attempt %d was throttled although we waited: %v", i+1, err)
		}
		if wait := w.nextTry.Sub(clock); wait > 0 {
			delays = append(delays, wait)
			clock = w.nextTry // wait exactly as long as asked
		}
	}
	if len(delays) == 0 || delays[0] != 2*time.Second || delays[1] != 4*time.Second {
		t.Errorf("delays should start at 2 s, 4 s...: %v", delays)
	}
	for _, d := range delays {
		if d > time.Minute {
			t.Errorf("delay above the cap: %v", d)
		}
	}
	if delays[len(delays)-1] != time.Minute {
		t.Errorf("the delay should reach the cap: %v", delays)
	}
}

func TestIdleTimeoutLocksAndActivityPostponesIt(t *testing.T) {
	v, _ := created(t)
	if v.IdleTimeout() != DefaultIdleTimeout {
		t.Errorf("default idle timeout: %v", v.IdleTimeout())
	}
	_ = v.Set("a@x", "secret")
	v.SetIdleTimeout(400 * time.Millisecond)

	// activity keeps it open well past one timeout
	for i := 0; i < 5; i++ {
		time.Sleep(150 * time.Millisecond)
		if _, err := v.Get("a@x"); err != nil {
			t.Fatalf("it locked while in use (after %d uses): %v", i, err)
		}
	}
	// inactivity locks it
	deadline := time.Now().Add(5 * time.Second)
	for v.Unlocked() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if v.Unlocked() {
		t.Fatal("the vault did not lock itself after the idle timeout")
	}
	if _, err := v.Get("a@x"); !errors.Is(err, ErrLocked) {
		t.Errorf("after the automatic lock: %v", err)
	}
	// 0 disables it
	if err := v.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	v.SetIdleTimeout(0)
	time.Sleep(600 * time.Millisecond)
	if !v.Unlocked() {
		t.Error("with the timeout disabled it must stay unlocked")
	}
}

func TestLockWipesTheKeys(t *testing.T) {
	v, _ := created(t)
	enc, mac := v.enc, v.mac
	if allZero(enc) || allZero(mac) {
		t.Fatal("the keys should not be zero while unlocked")
	}
	v.Lock()
	if !allZero(enc) || !allZero(mac) {
		t.Error("the key material must be overwritten on Lock")
	}
	if v.enc != nil || v.mac != nil {
		t.Error("the keys must be dropped")
	}
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return len(b) > 0
}

func TestConcurrentUse(t *testing.T) {
	v, _ := created(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acct := fmt.Sprintf("user%02d@ldap://h", i)
			if err := v.Set(acct, fmt.Sprintf("secret%02d", i)); err != nil {
				t.Errorf("Set: %v", err)
				return
			}
			if got, err := v.Get(acct); err != nil || got != fmt.Sprintf("secret%02d", i) {
				t.Errorf("Get(%s) = %q %v", acct, got, err)
			}
		}(i)
	}
	wg.Wait()
	accts, err := v.Accounts()
	if err != nil || len(accts) != 20 {
		t.Errorf("after 20 concurrent writers: %d accounts, %v", len(accts), err)
	}
}

func TestAccountsWithAnyCharacters(t *testing.T) {
	v, _ := created(t)
	for _, a := range []string{
		`cn=Smith\, John,ou=p@ldap://h`, "ñandú@ldap://日本語", "with\nnewline@x", `quote"and'apostrophe@x`, strings.Repeat("long", 500),
	} {
		if err := v.Set(a, "s:"+a); err != nil {
			t.Errorf("Set(%q): %v", a, err)
			continue
		}
		if got, err := v.Get(a); err != nil || got != "s:"+a {
			t.Errorf("Get(%q) = %q %v", a, got, err)
		}
	}
	if err := v.Set("", "x"); err == nil {
		t.Error("an empty account must be refused")
	}
}

func TestIDsAreStableAcrossHandlesButDependOnTheKey(t *testing.T) {
	v1, p1 := created(t)
	_ = v1.Set("a@x", "s")
	f1, _ := readFile(p1)
	v1.Lock()
	w := reopen(p1)
	_ = w.Unlock(pass)
	_ = w.Set("a@x", "s2") // same account: replaces, does not add
	f2, _ := readFile(p1)
	if len(f2.Entries) != 1 {
		t.Errorf("the same account must keep the same identifier: %d entries", len(f2.Entries))
	}
	// a different vault (different salt) gives different identifiers for the same account
	v3, p3 := created(t)
	_ = v3.Set("a@x", "s")
	f3, _ := readFile(p3)
	for id := range f3.Entries {
		if _, ok := f1.Entries[id]; ok {
			t.Error("identifiers must not be comparable between vaults")
		}
	}
}
