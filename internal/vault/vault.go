// Package vault is an encrypted file for secrets (LDAP passwords), protected by a
// master passphrase, for machines that have no system keychain.
//
// Design:
//   - Key: Argon2id(passphrase, random salt) -> 64 bytes: a 32-byte AES key and a
//     32-byte HMAC key. The parameters are stored in the file so they can be raised later.
//   - Each entry is sealed with AES-256-GCM under a fresh random nonce. Its
//     identifier is HMAC-SHA256(macKey, account): the file does not reveal which
//     accounts (user@server) exist, only how many. The identifier is also the AEAD
//     additional data, so entries cannot be swapped or renamed undetected.
//   - A "check" block lets a wrong passphrase be told from a damaged file.
//   - The derived keys live in memory only while unlocked and are wiped on Lock and
//     after an idle timeout. Failed unlocks are throttled.
//   - The file is written atomically with mode 0600.
//
// Limits (also documented in the manual): a Go string cannot be wiped from memory,
// so the passphrase and the secrets may remain in process memory until the garbage
// collector reuses it; and anyone who can run code as the same user while the vault
// is unlocked can read it.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	formatVersion = 1
	minPassphrase = 8
	checkPlain    = "ldap-explorer vault v1"
	keyLen        = 64 // 32 AES + 32 HMAC

	maxKDFTime      = 20
	maxKDFMemoryKiB = 1 << 20 // 1 GiB
	maxKDFThreads   = 16
	// DefaultIdleTimeout is how long the vault stays unlocked without being used.
	DefaultIdleTimeout = 15 * time.Minute
)

var (
	ErrNotFound        = errors.New("vault: no such secret")
	ErrNoVault         = errors.New("vault: it has not been created yet")
	ErrExists          = errors.New("vault: it already exists")
	ErrLocked          = errors.New("vault: it is locked")
	ErrWrongPassphrase = errors.New("vault: wrong passphrase")
	ErrWeakPassphrase  = fmt.Errorf("vault: the passphrase must have at least %d characters", minPassphrase)
	ErrCorrupt         = errors.New("vault: the file is damaged or was made by a newer version")
)

// TooManyAttemptsError is returned while unlocking is being throttled.
type TooManyAttemptsError struct{ RetryAfter time.Duration }

func (e *TooManyAttemptsError) Error() string {
	return fmt.Sprintf("vault: too many failed attempts, try again in %d s", int(e.RetryAfter.Round(time.Second)/time.Second))
}

// KDF holds the Argon2id cost. Tests lower it; production uses DefaultKDF.
type KDF struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memoryKiB"`
	Threads   uint8  `json:"threads"`
}

// DefaultKDF follows the OWASP recommendation for Argon2id (64 MiB, 3 passes).
var DefaultKDF = KDF{Time: 3, MemoryKiB: 64 * 1024, Threads: 2}

type sealed struct {
	Nonce string `json:"nonce"`
	CT    string `json:"ct"`
}

type file struct {
	Version int `json:"version"`
	KDF     struct {
		Name string `json:"name"`
		Salt string `json:"salt"`
		KDF
	} `json:"kdf"`
	Check   sealed            `json:"check"`
	Entries map[string]sealed `json:"entries"`
}

type entry struct {
	Account string `json:"account"`
	Secret  string `json:"secret"`
}

// Vault is safe for concurrent use.
type Vault struct {
	path string

	mu       sync.Mutex
	kdf      KDF // used by Create and ChangePassphrase
	enc, mac []byte
	timer    *time.Timer
	idle     time.Duration
	failures int
	nextTry  time.Time
	now      func() time.Time
}

// Open returns a handle for the vault at path. It does not read the file or unlock anything.
func Open(path string) *Vault {
	return &Vault{path: path, kdf: DefaultKDF, idle: DefaultIdleTimeout, now: time.Now}
}

// SetKDF changes the cost used for new or re-keyed vaults (tests use a cheap one).
func (v *Vault) SetKDF(k KDF) { v.mu.Lock(); v.kdf = k; v.mu.Unlock() }

// SetIdleTimeout sets how long an unlocked vault stays unlocked without use; 0 disables it.
func (v *Vault) SetIdleTimeout(d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.idle = d
	if v.enc != nil {
		v.armLocked()
	}
}

// IdleTimeout returns the configured idle timeout.
func (v *Vault) IdleTimeout() time.Duration { v.mu.Lock(); defer v.mu.Unlock(); return v.idle }

// Exists reports whether the vault file is there.
func (v *Vault) Exists() bool { _, err := os.Stat(v.path); return err == nil }

// Unlocked reports whether the keys are in memory.
func (v *Vault) Unlocked() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.enc != nil }

// Create makes a new vault protected by the passphrase and leaves it unlocked.
func (v *Vault) Create(passphrase string) error {
	if err := checkPassphrase(passphrase); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := os.Stat(v.path); err == nil {
		return ErrExists
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	enc, mac := derive(passphrase, salt, v.kdf)
	f := &file{Version: formatVersion, Entries: map[string]sealed{}}
	f.KDF.Name, f.KDF.Salt, f.KDF.KDF = "argon2id", b64(salt), v.kdf
	check, err := seal(enc, []byte(checkPlain), []byte("check"))
	if err != nil {
		return err
	}
	f.Check = check
	if err := writeAtomic(v.path, f); err != nil {
		wipe(enc)
		wipe(mac)
		return err
	}
	v.setKeysLocked(enc, mac)
	return nil
}

// Unlock derives the keys and checks them. Failed attempts are throttled.
func (v *Vault) Unlock(passphrase string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if wait := v.nextTry.Sub(v.now()); wait > 0 {
		return &TooManyAttemptsError{RetryAfter: wait}
	}
	f, err := readFile(v.path)
	if err != nil {
		return err
	}
	salt, err := unb64(f.KDF.Salt)
	if err != nil {
		return ErrCorrupt
	}
	enc, mac := derive(passphrase, salt, f.KDF.KDF)
	plain, err := open(enc, f.Check, []byte("check"))
	if err != nil || subtle.ConstantTimeCompare(plain, []byte(checkPlain)) != 1 {
		wipe(enc)
		wipe(mac)
		v.failures++
		// the first three tries are free, then 2, 4, 8... seconds up to a minute
		if v.failures > 3 {
			d := time.Duration(1<<min(v.failures-3, 6)) * time.Second
			v.nextTry = v.now().Add(min(d, time.Minute))
		}
		return ErrWrongPassphrase
	}
	v.failures, v.nextTry = 0, time.Time{}
	v.setKeysLocked(enc, mac)
	return nil
}

// Lock wipes the keys from memory.
func (v *Vault) Lock() { v.mu.Lock(); defer v.mu.Unlock(); v.lockLocked() }

func (v *Vault) lockLocked() {
	wipe(v.enc)
	wipe(v.mac)
	v.enc, v.mac = nil, nil
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
}

func (v *Vault) setKeysLocked(enc, mac []byte) {
	wipe(v.enc)
	wipe(v.mac)
	v.enc, v.mac = enc, mac
	v.armLocked()
}

// armLocked (re)starts the idle timer.
func (v *Vault) armLocked() {
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	if v.idle <= 0 {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(v.idle, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.timer == t { // not superseded by newer activity
			v.lockLocked()
		}
	})
	v.timer = t
}

// Set stores or replaces the secret of an account.
func (v *Vault) Set(account, secret string) error {
	if account == "" {
		return errors.New("vault: empty account")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.enc == nil {
		return ErrLocked
	}
	f, err := readFile(v.path)
	if err != nil {
		return err
	}
	plain, _ := json.Marshal(entry{Account: account, Secret: secret})
	id := v.idLocked(account)
	s, err := seal(v.enc, plain, []byte(id))
	if err != nil {
		return err
	}
	f.Entries[id] = s
	v.armLocked()
	return writeAtomic(v.path, f)
}

// Get returns the secret of an account.
func (v *Vault) Get(account string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.enc == nil {
		return "", ErrLocked
	}
	f, err := readFile(v.path)
	if err != nil {
		return "", err
	}
	id := v.idLocked(account)
	s, ok := f.Entries[id]
	if !ok {
		return "", ErrNotFound
	}
	e, err := v.openEntryLocked(id, s)
	if err != nil {
		return "", err
	}
	v.armLocked()
	return e.Secret, nil
}

// Has reports whether a secret exists for the account (the vault must be unlocked).
func (v *Vault) Has(account string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.enc == nil {
		return false, ErrLocked
	}
	f, err := readFile(v.path)
	if err != nil {
		return false, err
	}
	_, ok := f.Entries[v.idLocked(account)]
	return ok, nil
}

// Delete removes the secret of an account; deleting a missing one is not an error.
func (v *Vault) Delete(account string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.enc == nil {
		return ErrLocked
	}
	f, err := readFile(v.path)
	if err != nil {
		return err
	}
	delete(f.Entries, v.idLocked(account))
	v.armLocked()
	return writeAtomic(v.path, f)
}

// Accounts lists the accounts that have a secret, sorted.
func (v *Vault) Accounts() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.enc == nil {
		return nil, ErrLocked
	}
	f, err := readFile(v.path)
	if err != nil {
		return nil, err
	}
	var out []string
	for id, s := range f.Entries {
		e, err := v.openEntryLocked(id, s)
		if err != nil {
			return nil, err
		}
		out = append(out, e.Account)
	}
	sort.Strings(out)
	v.armLocked()
	return out, nil
}

// ChangePassphrase re-encrypts everything under a new passphrase (and a fresh salt).
func (v *Vault) ChangePassphrase(oldPass, newPass string) error {
	if err := checkPassphrase(newPass); err != nil {
		return err
	}
	if err := v.Unlock(oldPass); err != nil { // verifies the old one, with throttling
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	f, err := readFile(v.path)
	if err != nil {
		return err
	}
	// Decrypt everything first, so a failure leaves the file untouched.
	plains := map[string]entry{}
	for id, s := range f.Entries {
		e, err := v.openEntryLocked(id, s)
		if err != nil {
			return err
		}
		plains[id] = e
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	enc, mac := derive(newPass, salt, v.kdf)
	nf := &file{Version: formatVersion, Entries: map[string]sealed{}}
	nf.KDF.Name, nf.KDF.Salt, nf.KDF.KDF = "argon2id", b64(salt), v.kdf
	if nf.Check, err = seal(enc, []byte(checkPlain), []byte("check")); err != nil {
		return err
	}
	for _, e := range plains {
		id := entryID(mac, e.Account)
		plain, _ := json.Marshal(e)
		if nf.Entries[id], err = seal(enc, plain, []byte(id)); err != nil {
			return err
		}
	}
	if err := writeAtomic(v.path, nf); err != nil {
		wipe(enc)
		wipe(mac)
		return err
	}
	v.setKeysLocked(enc, mac)
	return nil
}

func (v *Vault) idLocked(account string) string { return entryID(v.mac, account) }

func entryID(mac []byte, account string) string {
	h := hmac.New(sha256.New, mac)
	h.Write([]byte(account))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (v *Vault) openEntryLocked(id string, s sealed) (entry, error) {
	plain, err := open(v.enc, s, []byte(id))
	if err != nil {
		return entry{}, ErrCorrupt
	}
	var e entry
	if err := json.Unmarshal(plain, &e); err != nil || entryID(v.mac, e.Account) != id {
		return entry{}, ErrCorrupt
	}
	return e, nil
}

// ---------- primitives ----------

func checkPassphrase(p string) error {
	if utf8.RuneCountInString(p) < minPassphrase {
		return ErrWeakPassphrase
	}
	return nil
}

func derive(passphrase string, salt []byte, k KDF) (enc, mac []byte) {
	key := argon2.IDKey([]byte(passphrase), salt, k.Time, k.MemoryKiB, k.Threads, keyLen)
	enc, mac = append([]byte(nil), key[:32]...), append([]byte(nil), key[32:]...)
	wipe(key)
	return
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func seal(key, plain, aad []byte) (sealed, error) {
	g, err := gcm(key)
	if err != nil {
		return sealed{}, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return sealed{}, err
	}
	return sealed{Nonce: b64(nonce), CT: b64(g.Seal(nil, nonce, plain, aad))}, nil
}

func open(key []byte, s sealed, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce, err1 := unb64(s.Nonce)
	ct, err2 := unb64(s.CT)
	if err1 != nil || err2 != nil || len(nonce) != g.NonceSize() {
		return nil, ErrCorrupt
	}
	return g.Open(nil, nonce, ct, aad)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func b64(b []byte) string            { return base64.RawStdEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }

// ---------- file ----------

func readFile(path string) (*file, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoVault
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Version != formatVersion || f.KDF.Name != "argon2id" ||
		f.KDF.Time == 0 || f.KDF.MemoryKiB == 0 || f.KDF.Threads == 0 ||
		// a tampered file must not be able to make us allocate gigabytes or loop for hours
		f.KDF.Time > maxKDFTime || f.KDF.MemoryKiB > maxKDFMemoryKiB || f.KDF.Threads > maxKDFThreads ||
		f.KDF.MemoryKiB < 8*uint32(f.KDF.Threads) {
		return nil, ErrCorrupt
	}
	if f.Entries == nil {
		f.Entries = map[string]sealed{}
	}
	return &f, nil
}

// writeAtomic writes through a temporary file in the same folder and renames it, so
// a crash cannot leave a half-written vault. The file is private to the user.
func writeAtomic(path string, f *file) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-*.tmp")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
