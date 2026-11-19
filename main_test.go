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

const testToken = "tok-de-prueba"

// keyringMocked indica que algún test ya sustituyó el llavero por el mock
// (keyring.MockInit no se puede deshacer en el mismo proceso).
var keyringMocked bool

// ---------- helpers ----------

// newTestServer arranca el handler real, con perfiles y llavero aislados.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := t.TempDir()
	// os.UserConfigDir mira estas según el SO.
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("AppData", cfg)
	keyring.MockInit()
	keyringMocked = true
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

// call hace una petición con token; devuelve estado y cuerpo decodificado en out (si no es nil).
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
			t.Fatalf("%s %s: respuesta no JSON (%d): %s", method, path, resp.StatusCode, raw)
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
		t.Fatalf("connect falló: %d %+v", code, r)
	}
	return r.Roots
}

// ---------- sin LDAP ----------

func TestAPIRequiresToken(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct{ name, token string }{{"sin token", ""}, {"token incorrecto", "otro"}} {
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
				t.Errorf("%s %s: esperaba 403, obtuve %d", tc.name, p, resp.StatusCode)
			}
		}
		req, _ := http.NewRequest("POST", srv.URL+"/api/connect", strings.NewReader("{}"))
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s POST connect: esperaba 403, obtuve %d", tc.name, resp.StatusCode)
		}
	}
	if code := call(t, srv, "GET", "/api/profiles", nil, nil); code != 200 {
		t.Errorf("con token: esperaba 200, obtuve %d", code)
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
		t.Fatalf("UI no servida: %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("CSP inesperada: %q", csp)
	}
}

func TestUIHasNoExternalResources(t *testing.T) {
	b, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?i)(src|href|action)\s*=\s*["']?\s*(https?:)?//|url\(\s*["']?(https?:)?//|@import\s+["']?(https?:)?//|fetch\(\s*["'` + "`" + `](https?:)?//`)
	if m := re.Find(b); m != nil {
		t.Errorf("ui.html referencia un recurso externo: %q", m)
	}
}

func TestProfilesCRUDNoPasswordOnDisk(t *testing.T) {
	srv := newTestServer(t)
	p := Profile{Name: "p1", URL: "ldap://h:389", BindDN: "cn=a,dc=x", BaseDN: "dc=x", UseKeychain: true}
	var list []Profile
	if code := call(t, srv, "POST", "/api/profiles", connectReq{p, "s3cr3t-pw"}, &list); code != 200 || len(list) != 1 {
		t.Fatalf("guardar: %d %+v", code, list)
	}
	// actualizar mismo nombre no duplica
	p.BaseDN = "dc=y"
	call(t, srv, "POST", "/api/profiles", connectReq{p, ""}, &list)
	if len(list) != 1 || list[0].BaseDN != "dc=y" {
		t.Fatalf("actualizar: %+v", list)
	}
	// la contraseña va al llavero con servicio y cuenta fijos, no al disco
	got, err := keyring.Get("ldapnav", "cn=a,dc=x@ldap://h:389")
	if err != nil || got != "s3cr3t-pw" {
		t.Fatalf("llavero: %q, %v", got, err)
	}
	raw, err := os.ReadFile(profilesPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("s3cr3t-pw")) {
		t.Error("profiles.json contiene la contraseña")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(profilesPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("permisos de profiles.json: %v", st.Mode().Perm())
		}
	}
	if code := call(t, srv, "POST", "/api/profiles", connectReq{Profile{}, ""}, &errResp{}); code != 400 {
		t.Errorf("perfil sin nombre: esperaba 400, obtuve %d", code)
	}
	if code := call(t, srv, "DELETE", "/api/profiles?name=p1", nil, &list); code != 200 || len(list) != 0 {
		t.Errorf("borrar: %d %+v", code, list)
	}
}

func TestKeychainAccountFormat(t *testing.T) {
	// Compatibilidad con contraseñas ya guardadas: cuenta = bindDN@url.
	if got := keychainAccount(Profile{BindDN: "cn=a,dc=x", URL: "ldaps://h"}); got != "cn=a,dc=x@ldaps://h" {
		t.Errorf("cuenta: %q", got)
	}
	if keychainService != "ldapnav" {
		t.Errorf("servicio: %q", keychainService)
	}
}

func TestKeychainMissingPassword(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	p := Profile{Name: "x", URL: "ldap://127.0.0.1:1", BindDN: "cn=a", UseKeychain: true}
	if code := call(t, srv, "POST", "/api/connect", connectReq{p, ""}, &e); code != 400 || !strings.Contains(e.Error, "llavero") {
		t.Errorf("esperaba error de llavero, obtuve %d %q", code, e.Error)
	}
}

func TestConnectValidationAndNotConnected(t *testing.T) {
	srv := newTestServer(t)
	var e errResp
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: "http://x"}, ""}, &e); code != 400 || !strings.Contains(e.Error, "ldap://") {
		t.Errorf("URL inválida: %d %q", code, e.Error)
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: "ldap://127.0.0.1:1"}, ""}, &e); code != 400 {
		t.Errorf("servidor inalcanzable: %d", code)
	}
	for _, c := range []struct{ m, p string }{{"GET", "/api/children?dn=x"}, {"GET", "/api/entry?dn=x"}, {"POST", "/api/search"}} {
		var body any
		if c.m == "POST" {
			body = map[string]string{}
		}
		if code := call(t, srv, c.m, c.p, body, &e); code != 400 || !strings.Contains(e.Error, "no conectado") {
			t.Errorf("%s sin sesión: %d %q", c.p, code, e.Error)
		}
	}
}

// ---------- invariantes de código ----------

func TestReadOnlyInvariant(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\.(Add|Modify|ModifyWithResult|ModifyDN|Del|PasswordModify|ModifyWithControls)\(`)
	if m := re.Find(b); m != nil {
		t.Errorf("main.go usa una operación de escritura LDAP: %q", m)
	}
	if !bytes.Contains(b, []byte(`"127.0.0.1:%d"`)) {
		t.Error("el servidor debe escuchar solo en 127.0.0.1")
	}
}

// Comprueba que se puede compilar sin CGO para los tres destinos soportados
// y que ninguna dependencia necesita cgo.
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
				t.Fatalf("falló la compilación: %v\n%s", err, out)
			}
			cmd = exec.Command("go", "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", ".")
			cmd.Env = env
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if s := strings.TrimSpace(string(out)); s != "" {
				t.Errorf("paquetes que usan cgo:\n%s", s)
			}
		})
	}
}
