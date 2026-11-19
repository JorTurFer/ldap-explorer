package main

// Where a profile's password lives: nowhere (typed every time), the system
// keychain, or the encrypted vault file (internal/vault). Passwords never go in
// profiles.json and are never returned by the API.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
	"ldap-explorer/internal/vault"
)

const (
	storeNone     = "none"
	storeKeychain = "keychain"
	storeVault    = "vault"
)

var errVaultLocked = errors.New("the vault is locked")

// ---------- profile normalisation ----------

// normalize makes Storage authoritative and keeps the old UseKeychain flag
// consistent (profiles saved by earlier versions only have UseKeychain).
func (p *Profile) normalize() {
	if p.Storage == "" {
		if p.UseKeychain {
			p.Storage = storeKeychain
		} else {
			p.Storage = storeNone
		}
	}
	p.UseKeychain = p.Storage == storeKeychain
}

func validStorage(s string) bool { return s == storeNone || s == storeKeychain || s == storeVault }

// ---------- the vault of this configuration folder ----------

var (
	vaultMu   sync.Mutex
	vaultInst *vault.Vault
	vaultPath string
	vaultKDF  = vault.DefaultKDF // tests lower it
)

// vaultFile returns the vault in the current configuration folder.
func vaultFile() *vault.Vault {
	path := filepath.Join(configDir(), "vault.json")
	vaultMu.Lock()
	defer vaultMu.Unlock()
	if vaultInst == nil || vaultPath != path {
		if vaultInst != nil {
			vaultInst.Lock() // never leave keys in memory for a folder we stopped using
		}
		vaultInst = vault.Open(path)
		vaultInst.SetKDF(vaultKDF)
		vaultPath = path
	}
	return vaultInst
}

// ---------- reading, writing and forgetting a profile's password ----------

func secretGet(p Profile) (string, error) {
	switch p.Storage {
	case storeKeychain:
		return keychainGet(p)
	case storeVault:
		pw, err := vaultFile().Get(keychainAccount(p))
		switch {
		case errors.Is(err, vault.ErrLocked):
			return "", errVaultLocked
		case errors.Is(err, vault.ErrNotFound), errors.Is(err, vault.ErrNoVault):
			return "", errors.New("no password in the vault for this profile")
		case err != nil:
			return "", fmt.Errorf("vault: %w", err)
		}
		return pw, nil
	}
	return "", errors.New("this profile does not save its password")
}

func secretSet(p Profile, pw string) error {
	switch p.Storage {
	case storeKeychain:
		return keychainSet(p, pw)
	case storeVault:
		if err := vaultFile().Set(keychainAccount(p), pw); errors.Is(err, vault.ErrLocked) {
			return errVaultLocked
		} else {
			return err
		}
	}
	return nil
}

// secretDelete forgets the stored password; a missing one is not an error.
func secretDelete(p Profile) error {
	switch p.Storage {
	case storeKeychain:
		for _, service := range []string{keychainService, legacyKeychainService} {
			if err := keyring.Delete(service, keychainAccount(p)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				return err
			}
		}
	case storeVault:
		return vaultFile().Delete(keychainAccount(p))
	}
	return nil
}

// keychainStatus says whether the system keychain can be used on this machine.
func keychainStatus() (bool, string) {
	_, err := keyring.Get(keychainService, "ldap-explorer availability check")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return true, ""
	}
	return false, err.Error()
}

// checkStorage verifies, before anything is saved, that the chosen store can take a password.
func checkStorage(p Profile) (status int, code string, err error) {
	switch p.Storage {
	case storeKeychain:
		if ok, reason := keychainStatus(); !ok {
			return http.StatusBadRequest, "keychain_unavailable", fmt.Errorf("the system keychain is not available here (%s): choose the encrypted vault or do not save the password", reason)
		}
	case storeVault:
		v := vaultFile()
		if !v.Exists() {
			return http.StatusBadRequest, "vault_missing", errors.New("the vault does not exist yet: create it with a master passphrase first")
		}
		if !v.Unlocked() {
			return http.StatusLocked, "vault_locked", errVaultLocked
		}
	}
	return 0, "", nil
}

// ---------- responses with a machine-readable code ----------

func writeCode(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": code})
}

// ---------- status ----------

type credStatus struct {
	Keychain struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
	} `json:"keychain"`
	Vault struct {
		Exists             bool `json:"exists"`
		Unlocked           bool `json:"unlocked"`
		IdleTimeoutSeconds int  `json:"idleTimeoutSeconds"`
	} `json:"vault"`
}

func currentStatus() credStatus {
	var s credStatus
	s.Keychain.Available, s.Keychain.Reason = keychainStatus()
	v := vaultFile()
	s.Vault.Exists, s.Vault.Unlocked = v.Exists(), v.Unlocked()
	s.Vault.IdleTimeoutSeconds = int(v.IdleTimeout().Seconds())
	return s
}

func handleCredStatus(w http.ResponseWriter, _ *http.Request) { writeJSON(w, currentStatus()) }

// ---------- the vault ----------

func vaultError(w http.ResponseWriter, err error) {
	var tm *vault.TooManyAttemptsError
	switch {
	case errors.As(err, &tm):
		w.Header().Set("Retry-After", strconv.Itoa(int(tm.RetryAfter.Seconds())+1))
		writeCode(w, http.StatusTooManyRequests, "throttled", err)
	case errors.Is(err, vault.ErrWrongPassphrase):
		writeCode(w, http.StatusBadRequest, "wrong_passphrase", errors.New("wrong passphrase"))
	case errors.Is(err, vault.ErrNoVault):
		writeCode(w, http.StatusBadRequest, "vault_missing", errors.New("the vault does not exist yet"))
	case errors.Is(err, vault.ErrExists):
		writeCode(w, http.StatusBadRequest, "vault_exists", errors.New("the vault already exists"))
	case errors.Is(err, vault.ErrWeakPassphrase):
		writeCode(w, http.StatusBadRequest, "weak_passphrase", err)
	case errors.Is(err, vault.ErrCorrupt):
		writeCode(w, http.StatusBadRequest, "vault_corrupt", errors.New("the vault file is damaged: it was not modified"))
	default:
		writeErr(w, err)
	}
}

type passphraseBody struct {
	Passphrase    string `json:"passphrase"`
	OldPassphrase string `json:"oldPassphrase"`
	NewPassphrase string `json:"newPassphrase"`
}

func handleVaultCreate(w http.ResponseWriter, r *http.Request) {
	var b passphraseBody
	if err := decode(r, &b); err != nil {
		writeErr(w, err)
		return
	}
	if err := vaultFile().Create(b.Passphrase); err != nil {
		vaultError(w, err)
		return
	}
	writeJSON(w, currentStatus())
}

func handleVaultUnlock(w http.ResponseWriter, r *http.Request) {
	var b passphraseBody
	if err := decode(r, &b); err != nil {
		writeErr(w, err)
		return
	}
	if err := vaultFile().Unlock(b.Passphrase); err != nil {
		vaultError(w, err)
		return
	}
	writeJSON(w, currentStatus())
}

func handleVaultLock(w http.ResponseWriter, _ *http.Request) {
	vaultFile().Lock()
	writeJSON(w, currentStatus())
}

func handleVaultPassphrase(w http.ResponseWriter, r *http.Request) {
	var b passphraseBody
	if err := decode(r, &b); err != nil {
		writeErr(w, err)
		return
	}
	if err := vaultFile().ChangePassphrase(b.OldPassphrase, b.NewPassphrase); err != nil {
		vaultError(w, err)
		return
	}
	writeJSON(w, currentStatus())
}

// ---------- which profiles have a saved password ----------

type credEntry struct {
	Profile string `json:"profile"`
	BindDN  string `json:"bindDN"`
	URL     string `json:"url"`
	Storage string `json:"storage"`
	State   string `json:"state"` // saved, missing, locked, unavailable, anonymous, none
}

type credList struct {
	Entries []credEntry `json:"entries"`
	// Orphans are vault entries no profile uses any more (known only while unlocked).
	Orphans []string `json:"orphans"`
}

func listCredentials() (*credList, error) {
	ps, err := loadProfiles()
	if err != nil {
		return nil, err
	}
	out := &credList{Entries: []credEntry{}, Orphans: []string{}}
	used := map[string]bool{}
	v := vaultFile()
	for _, p := range ps {
		e := credEntry{Profile: p.Name, BindDN: p.BindDN, URL: p.URL, Storage: p.Storage}
		switch {
		case p.Storage == storeNone:
			e.State = "none"
		case p.BindDN == "":
			e.State = "anonymous"
		case p.Storage == storeKeychain:
			if _, err := keychainGet(p); err == nil {
				e.State = "saved"
			} else if strings.Contains(err.Error(), "no password") {
				e.State = "missing"
			} else {
				e.State = "unavailable"
			}
		case p.Storage == storeVault:
			used[keychainAccount(p)] = true
			switch {
			case !v.Exists():
				e.State = "missing"
			case !v.Unlocked():
				e.State = "locked"
			default:
				if ok, _ := v.Has(keychainAccount(p)); ok {
					e.State = "saved"
				} else {
					e.State = "missing"
				}
			}
		}
		out.Entries = append(out.Entries, e)
	}
	if v.Unlocked() {
		if accts, err := v.Accounts(); err == nil {
			for _, a := range accts {
				if !used[a] {
					out.Orphans = append(out.Orphans, a)
				}
			}
		}
	}
	sort.Strings(out.Orphans)
	return out, nil
}

// handleCredentials lists (GET) the saved passwords' state and forgets one (DELETE):
// ?profile=NAME forgets that profile's password and stops saving it; ?account=X
// removes an orphaned vault entry.
func handleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		l, err := listCredentials()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, l)

	case http.MethodDelete:
		if acct := r.URL.Query().Get("account"); acct != "" {
			ps, err := loadProfiles()
			if err != nil {
				writeErr(w, err)
				return
			}
			for _, p := range ps { // never delete what a profile still uses
				if p.Storage == storeVault && keychainAccount(p) == acct {
					writeErr(w, errors.New("a profile still uses this entry: forget its password instead"))
					return
				}
			}
			if err := vaultFile().Delete(acct); errors.Is(err, vault.ErrLocked) {
				writeCode(w, http.StatusLocked, "vault_locked", errVaultLocked)
				return
			} else if err != nil {
				writeErr(w, err)
				return
			}
		} else {
			name := r.URL.Query().Get("profile")
			ps, err := loadProfiles()
			if err != nil {
				writeErr(w, err)
				return
			}
			found := false
			for i := range ps {
				if ps[i].Name != name {
					continue
				}
				found = true
				if err := secretDelete(ps[i]); errors.Is(err, vault.ErrLocked) {
					writeCode(w, http.StatusLocked, "vault_locked", errVaultLocked)
					return
				} else if err != nil {
					writeErr(w, err)
					return
				}
				ps[i].Storage = storeNone
				ps[i].normalize()
			}
			if !found {
				writeErr(w, errors.New("no such profile"))
				return
			}
			if err := saveProfiles(ps); err != nil {
				writeErr(w, err)
				return
			}
		}
		l, err := listCredentials()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, l)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
