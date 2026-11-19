// ldapnav: navegador LDAP mínimo, solo lectura, que corre en local.
//
// Sirve una UI web en 127.0.0.1 (puerto aleatorio) y abre el navegador.
// Todas las peticiones a la API exigen un token aleatorio generado al
// arrancar, para que ninguna otra web pueda usar el servidor local.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/zalando/go-keyring"
)

//go:embed ui.html
var uiFS embed.FS

// ---------- perfiles guardados (sin contraseña) ----------

type Profile struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	StartTLS   bool   `json:"startTLS"`
	SkipVerify bool   `json:"skipVerify"`
	BindDN     string `json:"bindDN"`
	BaseDN     string `json:"baseDN"`
	// Si es true, la contraseña se guarda/lee del llavero de macOS.
	UseKeychain bool `json:"useKeychain"`
}

func profilesPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ldapnav", "profiles.json")
}

func loadProfiles() ([]Profile, error) {
	b, err := os.ReadFile(profilesPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []Profile
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func saveProfiles(ps []Profile) error {
	p := profilesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(ps, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// ---------- llavero del sistema (go-keyring) ----------
// macOS: Keychain; Windows: Credential Manager; Linux: Secret Service.

const keychainService = "ldapnav"

func keychainAccount(p Profile) string { return p.BindDN + "@" + p.URL }

func keychainGet(p Profile) (string, error) {
	pw, err := keyring.Get(keychainService, keychainAccount(p))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", errors.New("no hay contraseña en el llavero para este perfil")
		}
		return "", fmt.Errorf("llavero: %w", err)
	}
	return pw, nil
}

func keychainSet(p Profile, pw string) error {
	return keyring.Set(keychainService, keychainAccount(p), pw)
}

// ---------- sesión LDAP ----------

type session struct {
	mu      sync.Mutex
	conn    *ldap.Conn
	profile Profile
}

var sess session

func dial(p Profile, password string) (*ldap.Conn, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: p.SkipVerify} //nolint:gosec // opción explícita del usuario
	if u := strings.TrimPrefix(strings.TrimPrefix(p.URL, "ldaps://"), "ldap://"); u != "" {
		host, _, err := net.SplitHostPort(u)
		if err != nil {
			host = u
		}
		tlsCfg.ServerName = host
	}
	c, err := ldap.DialURL(p.URL,
		ldap.DialWithTLSConfig(tlsCfg),
		ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}))
	if err != nil {
		return nil, err
	}
	c.SetTimeout(30 * time.Second)
	if p.StartTLS && strings.HasPrefix(p.URL, "ldap://") {
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return nil, fmt.Errorf("StartTLS: %w", err)
		}
	}
	if p.BindDN != "" {
		if err := c.Bind(p.BindDN, password); err != nil {
			c.Close()
			return nil, fmt.Errorf("bind: %w", err)
		}
	} else if err := c.UnauthenticatedBind(""); err != nil {
		c.Close()
		return nil, fmt.Errorf("bind anónimo: %w", err)
	}
	return c, nil
}

func (s *session) get() (*ldap.Conn, Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, Profile{}, errors.New("no conectado")
	}
	if s.conn.IsClosing() {
		return nil, Profile{}, errors.New("la conexión se ha cerrado; vuelve a conectar")
	}
	return s.conn, s.profile, nil
}

// ---------- tipos de respuesta ----------

type Attr struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
	Binary bool     `json:"binary,omitempty"`
}

type Entry struct {
	DN    string `json:"dn"`
	Attrs []Attr `json:"attrs,omitempty"`
}

func toEntry(e *ldap.Entry, withAttrs bool) Entry {
	out := Entry{DN: e.DN}
	if !withAttrs {
		return out
	}
	for _, a := range e.Attributes {
		at := Attr{Name: a.Name}
		bin := false
		for _, v := range a.ByteValues {
			if !isPrintable(v) {
				bin = true
				break
			}
		}
		if bin {
			at.Binary = true
			for _, v := range a.ByteValues {
				at.Values = append(at.Values, fmt.Sprintf("(binario, %d bytes) %s", len(v), hexPreview(v)))
			}
		} else {
			at.Values = a.Values
		}
		out.Attrs = append(out.Attrs, at)
	}
	sort.SliceStable(out.Attrs, func(i, j int) bool {
		// objectClass primero, luego alfabético.
		ai, aj := strings.EqualFold(out.Attrs[i].Name, "objectClass"), strings.EqualFold(out.Attrs[j].Name, "objectClass")
		if ai != aj {
			return ai
		}
		return strings.ToLower(out.Attrs[i].Name) < strings.ToLower(out.Attrs[j].Name)
	})
	return out
}

func isPrintable(b []byte) bool {
	for _, r := range string(b) {
		if r == 0xFFFD || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			return false
		}
	}
	return true
}

func hexPreview(b []byte) string {
	if len(b) > 16 {
		return hex.EncodeToString(b[:16]) + "…"
	}
	return hex.EncodeToString(b)
}

// ---------- handlers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ps, err := loadProfiles()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, ps)
	case http.MethodPost: // guardar/actualizar
		var req struct {
			Profile  Profile `json:"profile"`
			Password string  `json:"password"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		if req.Profile.Name == "" {
			writeErr(w, errors.New("el perfil necesita nombre"))
			return
		}
		ps, err := loadProfiles()
		if err != nil {
			writeErr(w, err)
			return
		}
		replaced := false
		for i := range ps {
			if ps[i].Name == req.Profile.Name {
				ps[i] = req.Profile
				replaced = true
			}
		}
		if !replaced {
			ps = append(ps, req.Profile)
		}
		if err := saveProfiles(ps); err != nil {
			writeErr(w, err)
			return
		}
		if req.Profile.UseKeychain && req.Password != "" {
			if err := keychainSet(req.Profile, req.Password); err != nil {
				writeErr(w, fmt.Errorf("perfil guardado, pero falló el llavero: %w", err))
				return
			}
		}
		writeJSON(w, ps)
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		ps, err := loadProfiles()
		if err != nil {
			writeErr(w, err)
			return
		}
		out := ps[:0]
		for _, p := range ps {
			if p.Name != name {
				out = append(out, p)
			}
		}
		if err := saveProfiles(out); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, out)
	default:
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
	}
}

func handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile  Profile `json:"profile"`
		Password string  `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	p := req.Profile
	if !strings.HasPrefix(p.URL, "ldap://") && !strings.HasPrefix(p.URL, "ldaps://") {
		writeErr(w, errors.New("la URL debe empezar por ldap:// o ldaps://"))
		return
	}
	pw := req.Password
	if pw == "" && p.UseKeychain && p.BindDN != "" {
		var err error
		if pw, err = keychainGet(p); err != nil {
			writeErr(w, err)
			return
		}
	}
	c, err := dial(p, pw)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Si no hay base DN, intenta sacarla del RootDSE.
	var contexts []string
	if rs, err := c.Search(ldap.NewSearchRequest("", ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 10, false, "(objectClass=*)", []string{"namingContexts", "defaultNamingContext"}, nil)); err == nil && len(rs.Entries) > 0 {
		if d := rs.Entries[0].GetAttributeValue("defaultNamingContext"); d != "" {
			contexts = append(contexts, d)
		}
		for _, nc := range rs.Entries[0].GetAttributeValues("namingContexts") {
			if !contains(contexts, nc) {
				contexts = append(contexts, nc)
			}
		}
	}
	if p.BaseDN != "" {
		contexts = append([]string{p.BaseDN}, without(contexts, p.BaseDN)...)
	}

	sess.mu.Lock()
	if sess.conn != nil {
		sess.conn.Close()
	}
	sess.conn, sess.profile = c, p
	sess.mu.Unlock()

	writeJSON(w, map[string]any{"ok": true, "roots": contexts})
}

func handleDisconnect(w http.ResponseWriter, _ *http.Request) {
	sess.mu.Lock()
	if sess.conn != nil {
		sess.conn.Close()
		sess.conn = nil
	}
	sess.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// Hijos directos de un DN (para el árbol). Paginado para no chocar con sizelimits.
func handleChildren(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	dn := r.URL.Query().Get("dn")
	sr := ldap.NewSearchRequest(dn, ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
		0, 60, false, "(objectClass=*)", []string{"1.1", "hasSubordinates", "numSubordinates"}, nil)
	res, err := c.SearchWithPaging(sr, 500)
	if err != nil && (res == nil || len(res.Entries) == 0) {
		writeErr(w, err)
		return
	}
	type child struct {
		DN      string `json:"dn"`
		RDN     string `json:"rdn"`
		Leaf    bool   `json:"leaf"`
		Unknown bool   `json:"unknown"`
	}
	out := make([]child, 0, len(res.Entries))
	for _, e := range res.Entries {
		ch := child{DN: e.DN, RDN: rdnOf(e.DN)}
		hs := e.GetAttributeValue("hasSubordinates")
		ns := e.GetAttributeValue("numSubordinates")
		switch {
		case strings.EqualFold(hs, "FALSE") || ns == "0":
			ch.Leaf = true
		case hs == "" && ns == "":
			ch.Unknown = true
		}
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].RDN) < strings.ToLower(out[j].RDN) })
	resp := map[string]any{"children": out}
	if err != nil {
		resp["warning"] = err.Error()
	}
	writeJSON(w, resp)
}

func handleEntry(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	dn := r.URL.Query().Get("dn")
	attrs := []string{"*"}
	if r.URL.Query().Get("operational") == "1" {
		attrs = append(attrs, "+")
	}
	res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 30, false, "(objectClass=*)", attrs, nil))
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(res.Entries) == 0 {
		writeErr(w, errors.New("entrada no encontrada"))
		return
	}
	writeJSON(w, toEntry(res.Entries[0], true))
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		Base   string `json:"base"`
		Filter string `json:"filter"`
		Scope  string `json:"scope"`
		Attrs  string `json:"attrs"`
		Limit  int    `json:"limit"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Filter == "" {
		req.Filter = "(objectClass=*)"
	}
	if _, err := ldap.CompileFilter(req.Filter); err != nil {
		writeErr(w, fmt.Errorf("filtro inválido: %w", err))
		return
	}
	scope := ldap.ScopeWholeSubtree
	switch req.Scope {
	case "base":
		scope = ldap.ScopeBaseObject
	case "one":
		scope = ldap.ScopeSingleLevel
	}
	var attrs []string
	for _, a := range strings.FieldsFunc(req.Attrs, func(r rune) bool { return r == ',' || r == ' ' }) {
		attrs = append(attrs, a)
	}
	if req.Limit <= 0 || req.Limit > 10000 {
		req.Limit = 500
	}
	sr := ldap.NewSearchRequest(req.Base, scope, ldap.NeverDerefAliases,
		req.Limit, 120, false, req.Filter, attrs, nil)
	res, err := c.SearchWithPaging(sr, uint32(min(req.Limit, 500)))
	var warning string
	if err != nil {
		if res == nil || len(res.Entries) == 0 {
			writeErr(w, err)
			return
		}
		warning = err.Error() // p.ej. size limit exceeded: devolvemos lo que haya
	}
	out := make([]Entry, 0, len(res.Entries))
	for _, e := range res.Entries {
		out = append(out, toEntry(e, true))
	}
	writeJSON(w, map[string]any{"entries": out, "warning": warning})
}

func rdnOf(dn string) string {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) == 0 {
		return dn
	}
	parts := make([]string, 0, len(d.RDNs[0].Attributes))
	for _, a := range d.RDNs[0].Attributes {
		parts = append(parts, a.Type+"="+a.Value)
	}
	return strings.Join(parts, "+")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func without(s []string, v string) []string {
	out := s[:0:0]
	for _, x := range s {
		if !strings.EqualFold(x, v) {
			out = append(out, x)
		}
	}
	return out
}

// ---------- arranque ----------

// newHandler construye el servidor HTTP: UI embebida en "/" y API bajo
// /api/*, que exige la cabecera X-Token. lastPing se actualiza con cada
// latido de la UI.
func newHandler(token string, lastPing *atomic.Int64) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("/api/profiles", handleProfiles)
	api.HandleFunc("POST /api/connect", handleConnect)
	api.HandleFunc("POST /api/disconnect", handleDisconnect)
	api.HandleFunc("GET /api/children", handleChildren)
	api.HandleFunc("GET /api/entry", handleEntry)
	api.HandleFunc("POST /api/search", handleSearch)

	// Latido: la UI hace ping periódicamente. Si se cierra la pestaña,
	// el proceso termina solo (útil cuando se lanza como .app).
	api.HandleFunc("POST /api/ping", func(w http.ResponseWriter, _ *http.Request) {
		lastPing.Store(time.Now().Unix())
		writeJSON(w, map[string]bool{"ok": true})
	})

	root := http.NewServeMux()
	root.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	}))
	root.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(b)
	})
	return root
}

func main() {
	port := flag.Int("port", 0, "puerto local (0 = aleatorio)")
	noOpen := flag.Bool("no-open", false, "no abrir el navegador automáticamente")
	idle := flag.Duration("idle-exit", 3*time.Minute, "salir si la UI deja de dar señales este tiempo (0 = nunca)")
	flag.Parse()

	tokBytes := make([]byte, 16)
	if _, err := rand.Read(tokBytes); err != nil {
		log.Fatal(err)
	}
	token := hex.EncodeToString(tokBytes)

	var lastPing atomic.Int64
	if *idle > 0 {
		go func() {
			for range time.Tick(5 * time.Second) {
				lp := lastPing.Load()
				if lp != 0 && time.Since(time.Unix(lp, 0)) > *idle {
					fmt.Println("UI cerrada, saliendo.")
					os.Exit(0)
				}
			}
		}()
	}
	handler := newHandler(token, &lastPing)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	// El token va en el fragmento (#), que el navegador nunca envía al servidor.
	url := fmt.Sprintf("http://%s/#%s", ln.Addr().String(), token)
	fmt.Println("ldapnav escuchando en", url)
	fmt.Println("Ctrl+C para salir.")
	if !*noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			switch runtime.GOOS {
			case "darwin":
				_ = exec.Command("open", url).Start()
			case "windows":
				_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
			default:
				_ = exec.Command("xdg-open", url).Start()
			}
		}()
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}
