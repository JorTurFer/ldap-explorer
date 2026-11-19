package main

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestUIServedWithCSP(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte("<html")) {
		t.Fatalf("UI not served: %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("unexpected CSP: %q", csp)
	}
}

func TestUIHasNoExternalResources(t *testing.T) {
	b, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?i)(src|href|action)\s*=\s*["']?\s*(https?:)?//|url\(\s*["']?(https?:)?//|@import\s+["']?(https?:)?//|fetch\(\s*["'` + "`" + `](https?:)?//`)
	if m := re.Find(b); m != nil {
		t.Errorf("ui.html references an external resource: %q", m)
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
	got, err := keyring.Get("ldapnav", "cn=a,dc=x@ldap://h:389")
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
	if keychainService != "ldapnav" {
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
