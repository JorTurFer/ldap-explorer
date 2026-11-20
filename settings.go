package main

// Look-and-feel settings (for now the theme), in settings.json next to profiles.json.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	Theme string `json:"theme"` // auto, light or dark
	// ChildHints: ask the server which entries have children, so the tree can show the ones without
	// as leaves. Off by default: on a huge branch that question is what makes a page slow, and the
	// tree then loads the names only (every entry shows the arrow).
	ChildHints bool `json:"childHints,omitempty"`
}

var settingsMu sync.Mutex

func settingsPath() string { return filepath.Join(configDir(), "settings.json") }

func loadSettings() (Settings, error) {
	s := Settings{Theme: "auto"}
	b, err := os.ReadFile(settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{Theme: "auto"}, err
	}
	if s.Theme != "light" && s.Theme != "dark" {
		s.Theme = "auto"
	}
	return s, nil
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	switch r.Method {
	case http.MethodGet:
		s, err := loadSettings()
		if err != nil {
			s = Settings{Theme: "auto"} // a broken settings file must never stop the app: use the defaults
		}
		writeJSON(w, s)
	case http.MethodPost:
		// Each field is optional: the theme button sends only the theme, the tree option only its own.
		var in struct {
			Theme      *string `json:"theme"`
			ChildHints *bool   `json:"childHints"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		s, _ := loadSettings() // a broken file starts again from the defaults
		if in.Theme != nil {
			if *in.Theme != "auto" && *in.Theme != "light" && *in.Theme != "dark" {
				writeErr(w, errors.New("theme must be auto, light or dark"))
				return
			}
			s.Theme = *in.Theme
		}
		if in.ChildHints != nil {
			s.ChildHints = *in.ChildHints
		}
		if err := writePrivateJSON(settingsPath(), s); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, s)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// writePrivateJSON writes v as JSON atomically (temporary file, then rename), mode 0600.
func writePrivateJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*.tmp")
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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
