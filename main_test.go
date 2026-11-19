package main

import (
	"bytes"
	"encoding/json"
	"io"
	"ldap-explorer/internal/slapdtest"
	"ldap-explorer/internal/vault"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain shuts down the shared slapd (see internal/slapdtest) after the tests.
func TestMain(m *testing.M) {
	code := m.Run()
	slapdtest.Stop()
	os.Exit(code)
}

const testToken = "test-token"

// keyringMocked indicates that some test has already replaced the keychain with
// the mock (keyring.MockInit cannot be undone in the same process).
var keyringMocked bool

// ---------- helpers ----------

// newTestServer starts the real handler, with isolated profiles and keychain.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := t.TempDir()
	// os.UserConfigDir looks at these depending on the OS.
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("AppData", cfg)
	keyring.MockInit()
	keyringMocked = true
	vaultKDF = vault.KDF{Time: 1, MemoryKiB: 8, Threads: 1} // the real cost would only slow the tests down
	return serveHandler(t)
}

// serveHandler starts the real handler and closes the LDAP session on cleanup.
func serveHandler(t *testing.T) *httptest.Server {
	t.Helper()
	var lp atomic.Int64
	srv := httptest.NewServer(newHandler(testToken, &lp))
	t.Cleanup(func() {
		sess.mu.Lock()
		if sess.conn != nil {
			sess.conn.Close()
			sess.conn = nil
		}
		sess.mu.Unlock()
		srv.Close()
	})
	return srv
}

// call makes a request with the token; it returns the status and the body decoded into out (if not nil).
func call(t *testing.T, srv *httptest.Server, method, path string, body, out any) int {
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
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: non-JSON response (%d): %s", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode
}

type errResp struct {
	Error string `json:"error"`
}

type connectReq struct {
	Profile  Profile `json:"profile"`
	Password string  `json:"password"`
}

type entryResp struct {
	DN    string `json:"dn"`
	Attrs []Attr `json:"attrs"`
}

func (e entryResp) attr(name string) *Attr {
	for i := range e.Attrs {
		if strings.EqualFold(e.Attrs[i].Name, name) {
			return &e.Attrs[i]
		}
	}
	return nil
}

func connectOK(t *testing.T, srv *httptest.Server, p Profile, pw string) []string {
	t.Helper()
	var r struct {
		OK    bool     `json:"ok"`
		Roots []string `json:"roots"`
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, pw}, &r); code != 200 || !r.OK {
		t.Fatalf("connect failed: %d %+v", code, r)
	}
	return r.Roots
}

// ---------- without LDAP ----------

func TestAPIRequiresToken(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct{ name, token string }{{"no token", ""}, {"wrong token", "other"}} {
		for _, p := range []string{"/api/profiles", "/api/children?dn=x", "/api/entry?dn=x"} {
			req, _ := http.NewRequest("GET", srv.URL+p, nil)
			if tc.token != "" {
				req.Header.Set("X-Token", tc.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 403 {
				t.Errorf("%s %s: expected 403, got %d", tc.name, p, resp.StatusCode)
			}
		}
		req, _ := http.NewRequest("POST", srv.URL+"/api/connect", strings.NewReader("{}"))
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s POST connect: expected 403, got %d", tc.name, resp.StatusCode)
		}
	}
	if code := call(t, srv, "GET", "/api/profiles", nil, nil); code != 200 {
		t.Errorf("with token: expected 200, got %d", code)
	}
}

func TestUIServedWithStrictCSP(t *testing.T) {
	srv := newTestServer(t)
	get := func(path string) (*http.Response, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	resp, body := get("/")
	if resp.StatusCode != 200 || !strings.Contains(body, "<html") {
		t.Fatalf("UI not served: %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "http") {
		t.Errorf("CSP is too permissive: %s", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Error("missing security headers")
	}

	// Every asset index.html references is served, with the type browsers require
	// for module scripts and stylesheets.
	for path, wantType := range map[string]string{
		"/js/app.js":        "text/javascript",
		"/css/style.css":    "text/css",
		"/js/api.js":        "text/javascript",
		"/js/connection.js": "text/javascript",
	} {
		r, b := get(path)
		if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), wantType) || len(b) == 0 {
			t.Errorf("%s: status %d, content-type %q", path, r.StatusCode, r.Header.Get("Content-Type"))
		}
	}
	if r, _ := get("/js/nothing.js"); r.StatusCode != 404 {
		t.Errorf("unknown file: %d", r.StatusCode)
	}
	// tests under front/test are not embedded
	if r, _ := get("/test/table.test.js"); r.StatusCode != 404 {
		t.Errorf("front/test must not be served: %d", r.StatusCode)
	}
}

// walkFront calls fn for every UI source file (HTML, CSS, JS) under front/.
func walkFront(t *testing.T, fn func(path, content string)) {
	t.Helper()
	err := filepath.WalkDir("front", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".css", ".js":
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fn(filepath.ToSlash(p), string(b))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUIHasNoExternalResources(t *testing.T) {
	external := regexp.MustCompile(`(?i)(src|href|action)\s*=\s*["']?\s*(https?:)?//|url\(\s*["']?(https?:)?//|@import\s+["']?(https?:)?//|fetch\(\s*["'` + "`" + `](https?:)?//|from\s+["']https?:|import\(\s*["']https?:|new\s+(WebSocket|EventSource)\(`)
	n := 0
	walkFront(t, func(path, content string) {
		n++
		if m := external.FindString(content); m != "" {
			t.Errorf("%s references an external resource: %q", path, m)
		}
	})
	if n < 10 {
		t.Errorf("only %d UI files found: is front/ complete?", n)
	}
}

// The strict CSP (no unsafe-inline) only works if the page has no inline code.
func TestUIHasNoInlineCode(t *testing.T) {
	html, err := os.ReadFile("front/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for name, re := range map[string]*regexp.Regexp{
		"inline <script> body": regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>[^<]*\S[^<]*</script>`),
		"<style> block":        regexp.MustCompile(`(?i)<style`),
		"style attribute":      regexp.MustCompile(`(?i)\sstyle\s*=`),
		"inline event handler": regexp.MustCompile(`(?i)\son[a-z]+\s*=`),
		"javascript: URL":      regexp.MustCompile(`(?i)javascript:`),
	} {
		if m := re.Find(html); m != nil {
			t.Errorf("index.html has %s: %q", name, m)
		}
	}
	// setAttribute("style") and innerHTML would also bypass or fight the policy.
	walkFront(t, func(path, content string) {
		if strings.HasSuffix(path, ".js") && regexp.MustCompile(`setAttribute\(\s*["']style|\.innerHTML\s*=|\beval\(|new Function\(`).MatchString(content) {
			t.Errorf("%s uses an unsafe construct (setAttribute style / innerHTML / eval)", path)
		}
	})
}

// A tag that is opened and never closed silently reshapes the page (this once
// swallowed a whole tab), so index.html must be balanced.
func TestIndexHTMLIsBalanced(t *testing.T) {
	b, err := os.ReadFile("front/index.html")
	if err != nil {
		t.Fatal(err)
	}
	void := map[string]bool{"meta": true, "link": true, "input": true, "br": true, "img": true, "hr": true}
	tag := regexp.MustCompile(`(?s)<!--.*?-->|<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*?(/?)>`)
	var stack []string
	for _, m := range tag.FindAllStringSubmatch(string(b), -1) {
		if strings.HasPrefix(m[0], "<!--") {
			continue
		}
		name := strings.ToLower(m[2])
		switch {
		case void[name] || m[3] == "/":
		case m[1] == "/":
			if len(stack) == 0 || stack[len(stack)-1] != name {
				t.Fatalf("</%s> does not match the open tags %v", name, stack)
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, name)
		}
	}
	if len(stack) != 0 {
		t.Errorf("unclosed tags in index.html: %v", stack)
	}
}

// Every relative import and every asset referenced from index.html must exist.
func TestUIReferencesResolve(t *testing.T) {
	imp := regexp.MustCompile(`(?m)(?:from|import)\s*\(?\s*["'](\.{1,2}/[^"']+)["']`)
	walkFront(t, func(path, content string) {
		if !strings.HasSuffix(path, ".js") {
			return
		}
		for _, m := range imp.FindAllStringSubmatch(content, -1) {
			target := filepath.Join(filepath.Dir(path), m[1])
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s imports %s, which does not exist", path, m[1])
			}
		}
	})
	html, _ := os.ReadFile("front/index.html")
	for _, m := range regexp.MustCompile(`(?:src|href)="([^"#]+)"`).FindAllStringSubmatch(string(html), -1) {
		if strings.HasPrefix(m[1], "data:") {
			continue
		}
		if _, err := os.Stat(filepath.Join("front", m[1])); err != nil {
			t.Errorf("index.html references %s, which does not exist", m[1])
		}
	}
}

func TestProfilesCRUDNoPasswordOnDisk(t *testing.T) {
	srv := newTestServer(t)
	p := Profile{Name: "p1", URL: "ldap://h:389", BindDN: "cn=a,dc=x", BaseDN: "dc=x", UseKeychain: true}
	var list []Profile
	if code := call(t, srv, "POST", "/api/profiles", connectReq{p, "s3cr3t-pw"}, &list); code != 200 || len(list) != 1 {
		t.Fatalf("save: %d %+v", code, list)
	}
	// updating the same name does not duplicate
	p.BaseDN = "dc=y"
	call(t, srv, "POST", "/api/profiles", connectReq{p, ""}, &list)
	if len(list) != 1 || list[0].BaseDN != "dc=y" {
		t.Fatalf("update: %+v", list)
	}
	// the password goes to the keychain with a fixed service and account, not to disk
	got, err := keyring.Get("ldap-explorer", "cn=a,dc=x@ldap://h:389")
	if err != nil || got != "s3cr3t-pw" {
		t.Fatalf("keychain: %q, %v", got, err)
	}
	raw, err := os.ReadFile(profilesPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("s3cr3t-pw")) {
		t.Error("profiles.json contains the password")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(profilesPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("profiles.json permissions: %v", st.Mode().Perm())
		}
	}
	if code := call(t, srv, "POST", "/api/profiles", connectReq{Profile{}, ""}, &errResp{}); code != 400 {
		t.Errorf("profile without a name: expected 400, got %d", code)
	}
	if code := call(t, srv, "DELETE", "/api/profiles?name=p1", nil, &list); code != 200 || len(list) != 0 {
		t.Errorf("delete: %d %+v", code, list)
	}
}

func TestKeychainAccountFormat(t *testing.T) {
	// Compatibility with already stored passwords: account = bindDN@url.
	if got := keychainAccount(Profile{BindDN: "cn=a,dc=x", URL: "ldaps://h"}); got != "cn=a,dc=x@ldaps://h" {
		t.Errorf("account: %q", got)
	}
	if keychainService != "ldap-explorer" {
		t.Errorf("service: %q", keychainService)
	}
}

func TestKeychainMissingPassword(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	p := Profile{Name: "x", URL: "ldap://127.0.0.1:1", BindDN: "cn=a", UseKeychain: true}
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, ""}, &e); code != 400 || !strings.Contains(e.Error, "keychain") {
		t.Errorf("expected a keychain error, got %d %q", code, e.Error)
	}
}

func TestConnectValidationAndNotConnected(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: "http://x"}, ""}, &e); code != 400 || !strings.Contains(e.Error, "ldap://") {
		t.Errorf("invalid URL: %d %q", code, e.Error)
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: "ldap://127.0.0.1:1"}, ""}, &e); code != 400 {
		t.Errorf("unreachable server: %d", code)
	}
	for _, c := range []struct{ m, p string }{{"GET", "/api/children?dn=x"}, {"GET", "/api/entry?dn=x"}, {"POST", "/api/search"}} {
		var body any
		if c.m == "POST" {
			body = map[string]string{}
		}
		if code := call(t, srv, c.m, c.p, body, &e); code != 400 || !strings.Contains(e.Error, "not connected") {
			t.Errorf("%s without a session: %d %q", c.p, code, e.Error)
		}
	}
}

// ---------- code invariants ----------

func TestReadOnlyInvariant(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\.(Add|Modify|ModifyWithResult|ModifyDN|Del|PasswordModify|ModifyWithControls)\(`)
	if m := re.Find(b); m != nil {
		t.Errorf("main.go uses an LDAP write operation: %q", m)
	}
	if !bytes.Contains(b, []byte(`"127.0.0.1:%d"`)) {
		t.Error("the server must listen only on 127.0.0.1")
	}
}

// Checks that it builds without CGO for the three supported targets
// and that no dependency needs cgo.
func TestBuildsWithoutCGO(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, target := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"windows", "amd64"}} {
		t.Run(target[0]+"_"+target[1], func(t *testing.T) {
			env := append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target[0], "GOARCH="+target[1])
			cmd := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "out"), ".")
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, out)
			}
			cmd = exec.Command("go", "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", ".")
			cmd.Env = env
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if s := strings.TrimSpace(string(out)); s != "" {
				t.Errorf("packages that use cgo:\n%s", s)
			}
		})
	}
}

// Passwords saved by the versions called ldapnav must keep working.
func TestLegacyKeychainEntriesStillWork(t *testing.T) {
	keyring.MockInit()
	keyringMocked = true
	p := Profile{BindDN: "cn=a,dc=x", URL: "ldap://h"}
	if _, err := keychainGet(p); err == nil || !strings.Contains(err.Error(), "no password in the keychain") {
		t.Fatalf("nothing saved yet: %v", err)
	}
	if err := keyring.Set("ldapnav", keychainAccount(p), "oldpw"); err != nil {
		t.Fatal(err)
	}
	got, err := keychainGet(p)
	if err != nil || got != "oldpw" {
		t.Fatalf("legacy entry: %q %v", got, err)
	}
	// it was copied to the new service, and the old entry was not touched
	if v, err := keyring.Get(keychainService, keychainAccount(p)); err != nil || v != "oldpw" {
		t.Errorf("not copied to the new service: %q %v", v, err)
	}
	if v, err := keyring.Get("ldapnav", keychainAccount(p)); err != nil || v != "oldpw" {
		t.Errorf("the legacy entry must stay: %q %v", v, err)
	}
	// a newer password wins over the legacy one
	if err := keychainSet(p, "newpw"); err != nil {
		t.Fatal(err)
	}
	if got, _ := keychainGet(p); got != "newpw" {
		t.Errorf("new entry should win: %q", got)
	}
}

func TestLegacyConfigFolderIsMigrated(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("AppData", cfg)
	old := filepath.Join(filepath.Dir(configDir()), "ldapnav")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	profiles := `[{"name":"lab","url":"ldap://h:389","bindDN":"cn=a","baseDN":"","startTLS":false,"skipVerify":false,"useKeychain":true}]`
	for name, content := range map[string]string{"profiles.json": profiles, "queries.json": "[]"} {
		if err := os.WriteFile(filepath.Join(old, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	migrateLegacyConfig()
	if _, err := os.Stat(old); err == nil {
		t.Error("the old folder should be gone")
	}
	ps, err := loadProfiles()
	if err != nil || len(ps) != 1 || ps[0].Name != "lab" || !ps[0].UseKeychain {
		t.Fatalf("profiles after the migration: %+v %v", ps, err)
	}
	if b, _ := os.ReadFile(queriesPath()); string(b) != "[]" {
		t.Errorf("queries.json: %q", b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(profilesPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("permissions were not kept: %v", st.Mode().Perm())
		}
	}

	// Running it again is harmless, and it never overwrites a folder that already exists.
	migrateLegacyConfig()
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "profiles.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyConfig()
	if ps, _ := loadProfiles(); len(ps) != 1 {
		t.Errorf("an existing new folder must not be overwritten: %+v", ps)
	}
	if b, _ := os.ReadFile(filepath.Join(old, "profiles.json")); string(b) != "[]" {
		t.Error("the old folder must be left alone when the new one exists")
	}
}
