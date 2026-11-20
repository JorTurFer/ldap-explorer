package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettings(t *testing.T) {
	srv := newTestServer(t)
	var s Settings
	if code := call(t, srv, "GET", "/api/settings", nil, &s); code != 200 || s.Theme != "auto" {
		t.Fatalf("default: %d %+v", code, s)
	}
	for _, theme := range []string{"dark", "light", "auto"} {
		if code := call(t, srv, "POST", "/api/settings", Settings{Theme: theme}, &s); code != 200 || s.Theme != theme {
			t.Fatalf("save %s: %d %+v", theme, code, s)
		}
		var got Settings
		call(t, srv, "GET", "/api/settings", nil, &got)
		if got.Theme != theme {
			t.Errorf("persisted theme: %q, want %q", got.Theme, theme)
		}
	}
	var e errResp
	for _, bad := range []string{"purple", "", "DARK"} {
		if code := call(t, srv, "POST", "/api/settings", Settings{Theme: bad}, &e); code != 400 {
			t.Errorf("theme %q should be refused: %d", bad, code)
		}
	}
	// a refused value did not change the stored one
	call(t, srv, "POST", "/api/settings", Settings{Theme: "dark"}, &s)
	call(t, srv, "POST", "/api/settings", Settings{Theme: "purple"}, &e)
	var got Settings
	call(t, srv, "GET", "/api/settings", nil, &got)
	if got.Theme != "dark" {
		t.Errorf("a refused value changed the setting: %q", got.Theme)
	}
}

func TestSettingsFileIsPrivateAndAtomic(t *testing.T) {
	srv := newTestServer(t)
	call(t, srv, "POST", "/api/settings", Settings{Theme: "dark"}, &Settings{})
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(settingsPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("permissions: %v", st.Mode().Perm())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(settingsPath()), ".write-*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

// A broken settings file must never stop the app: use the defaults.
func TestBrokenSettingsFileFallsBackToTheDefaults(t *testing.T) {
	srv := newTestServer(t)
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"{not json", `{"theme":"neon"}`, ""} {
		if err := os.WriteFile(settingsPath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var s Settings
		if code := call(t, srv, "GET", "/api/settings", nil, &s); code != 200 || s.Theme != "auto" {
			t.Errorf("content %q: %d %+v", content, code, s)
		}
	}
}

// Each setting is saved on its own: choosing the theme must not undo the tree option, and the
// other way round (the two controls send only their own field).
func TestSettingsAreMergedNotReplaced(t *testing.T) {
	srv := newTestServer(t)
	var s Settings
	call(t, srv, "POST", "/api/settings", map[string]any{"childHints": true}, &s)
	if !s.ChildHints || s.Theme != "auto" {
		t.Fatalf("after the tree option: %+v", s)
	}
	call(t, srv, "POST", "/api/settings", map[string]any{"theme": "dark"}, &s)
	if !s.ChildHints || s.Theme != "dark" {
		t.Fatalf("choosing the theme must keep the tree option: %+v", s)
	}
	call(t, srv, "POST", "/api/settings", map[string]any{"childHints": false}, &s)
	var got Settings
	call(t, srv, "GET", "/api/settings", nil, &got)
	if got.ChildHints || got.Theme != "dark" {
		t.Errorf("turning the option off keeps the theme: %+v", got)
	}
	// off by default
	if (Settings{}).ChildHints {
		t.Error("the child hints are off by default")
	}
}
