package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// Tests contra un OpenLDAP real (slapd efímero, ver slapd_test.go).

func alice(l *testLDAP) Profile {
	return Profile{Name: "alice", URL: l.URL, BindDN: testAliceDN}
}

func TestLDAPConnectAndRoots(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)

	roots := connectOK(t, srv, alice(l), testAlicePW)
	if len(roots) == 0 || roots[0] != testBaseDN {
		t.Errorf("roots (namingContexts del RootDSE): %v", roots)
	}
	// BaseDN explícito va primero
	p := alice(l)
	p.BaseDN = "ou=people," + testBaseDN
	if roots := connectOK(t, srv, p, testAlicePW); roots[0] != p.BaseDN {
		t.Errorf("roots con BaseDN: %v", roots)
	}

	var e errResp
	if code := call(t, srv, "POST", "/api/connect", connectReq{alice(l), "mala"}, &e); code != 400 || !strings.Contains(e.Error, "bind") {
		t.Errorf("bind incorrecto: %d %q", code, e.Error)
	}
	// un bind fallido no debe tumbar la sesión ya abierta
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &entryResp{}); code != 200 {
		t.Errorf("sesión previa perdida tras bind fallido: %d", code)
	}
	if code := call(t, srv, "POST", "/api/disconnect", nil, nil); code != 200 {
		t.Errorf("disconnect: %d", code)
	}
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &e); code != 400 || !strings.Contains(e.Error, "no conectado") {
		t.Errorf("tras disconnect: %d %q", code, e.Error)
	}
}

func TestLDAPAnonymousAndAdminBind(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, Profile{URL: l.URL}, "")
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape(testBaseDN), nil, &entryResp{}); code != 200 {
		t.Errorf("lectura anónima: %d", code)
	}
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testAdminDN}, testAdminPW)
}

func TestLDAPPasswordFromKeychain(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	p := alice(l)
	p.UseKeychain = true
	// guardar perfil con contraseña -> llavero; conectar sin contraseña la usa
	var list []Profile
	if code := call(t, srv, "POST", "/api/profiles", connectReq{p, testAlicePW}, &list); code != 200 {
		t.Fatalf("guardar perfil: %d", code)
	}
	connectOK(t, srv, p, "")

	// Contraseña guardada por una versión anterior (texto plano, misma cuenta): sigue valiendo.
	p2 := Profile{Name: "legacy", URL: l.URL, BindDN: testAliceDN + "", UseKeychain: true}
	p2.URL = l.URL + "/" // otra cuenta distinta
	if err := keyring.Set(keychainService, keychainAccount(p2), testAlicePW); err != nil {
		t.Fatal(err)
	}
	connectOK(t, srv, p2, "")
}

func TestLDAPTLS(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	var e errResp

	// cert autofirmado: debe fallar verificando...
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: l.LDAPSURL, BindDN: testAliceDN}, testAlicePW}, &e); code != 400 {
		t.Errorf("ldaps sin skipVerify debería fallar con cert autofirmado: %d", code)
	}
	if code := call(t, srv, "POST", "/api/connect", connectReq{Profile{URL: l.URL, StartTLS: true, BindDN: testAliceDN}, testAlicePW}, &e); code != 400 {
		t.Errorf("StartTLS sin skipVerify debería fallar: %d", code)
	}
	// ...y funcionar con skipVerify
	connectOK(t, srv, Profile{URL: l.LDAPSURL, SkipVerify: true, BindDN: testAliceDN}, testAlicePW)
	connectOK(t, srv, Profile{URL: l.URL, StartTLS: true, SkipVerify: true, BindDN: testAliceDN}, testAlicePW)
}

func TestLDAPTreeChildren(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)

	type child struct {
		DN, RDN       string
		Leaf, Unknown bool
	}
	var r struct {
		Children []child `json:"children"`
		Warning  string  `json:"warning"`
	}
	get := func(dn string) {
		r.Children, r.Warning = nil, ""
		if code := call(t, srv, "GET", "/api/children?dn="+url.QueryEscape(dn), nil, &r); code != 200 {
			t.Fatalf("children %s: %d", dn, code)
		}
	}

	get(testBaseDN)
	var rdns []string
	byRDN := map[string]child{}
	for _, c := range r.Children {
		rdns = append(rdns, c.RDN)
		byRDN[c.RDN] = c
	}
	want := []string{"ou=bulk", "ou=empty", "ou=groups", "ou=people"} // ordenados
	if strings.Join(rdns, ",") != strings.Join(want, ",") {
		t.Fatalf("hijos de la raíz: %v", rdns)
	}
	if !byRDN["ou=empty"].Leaf {
		t.Error("ou=empty debería marcarse como hoja (hasSubordinates=FALSE)")
	}
	if byRDN["ou=people"].Leaf || byRDN["ou=people"].Unknown {
		t.Errorf("ou=people debería tener hijos: %+v", byRDN["ou=people"])
	}

	get("ou=people," + testBaseDN)
	if len(r.Children) != 3 || !r.Children[0].Leaf {
		t.Errorf("hijos de people: %+v", r.Children)
	}

	// paginación: más entradas (600) que el tamaño de página (500). Bob no tiene
	// límite de tamaño en el servidor, así que debe verlas todas sin aviso.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testBobDN}, testBobPW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) != bulkCount || r.Warning != "" {
		t.Errorf("paginación: %d hijos (esperaba %d), warning=%q", len(r.Children), bulkCount, r.Warning)
	}

	// Zoe solo ve más de 100 resultados si la búsqueda es paginada: comprueba
	// que children usa el control de paginación.
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testZoeDN}, testZoePW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) != bulkCount || r.Warning != "" {
		t.Errorf("paginación (usuario con límite 100 no paginado): %d hijos, warning=%q", len(r.Children), r.Warning)
	}

	// Alice tiene el límite del servidor (550): resultados parciales + aviso, no error.
	connectOK(t, srv, alice(l), testAlicePW)
	get("ou=bulk," + testBaseDN)
	if len(r.Children) == 0 || len(r.Children) >= bulkCount || r.Warning == "" {
		t.Errorf("límite del servidor: %d hijos, warning=%q", len(r.Children), r.Warning)
	}
}

func TestLDAPEntry(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)
	q := func(dn string, op bool) (entryResp, int) {
		var e entryResp
		path := "/api/entry?dn=" + url.QueryEscape(dn)
		if op {
			path += "&operational=1"
		}
		code := call(t, srv, "GET", path, nil, &e)
		return e, code
	}

	e, code := q(testAliceDN, false)
	if code != 200 || e.DN == "" {
		t.Fatalf("entry: %d %+v", code, e)
	}
	if e.Attrs[0].Name != "objectClass" {
		t.Errorf("objectClass debería ir primero: %v", e.Attrs[0].Name)
	}
	if a := e.attr("mail"); a == nil || a.Values[0] != "alice@example.org" {
		t.Errorf("mail: %+v", a)
	}
	if a := e.attr("jpegPhoto"); a == nil || !a.Binary || !strings.Contains(a.Values[0], "binario") {
		t.Errorf("jpegPhoto debería resumirse como binario: %+v", a)
	}
	if e.attr("entryUUID") != nil {
		t.Error("sin operational=1 no deberían venir atributos operacionales")
	}
	if e, _ := q(testAliceDN, true); e.attr("entryUUID") == nil {
		t.Error("con operational=1 debería venir entryUUID")
	}

	// no ASCII (UTF-8)
	if e, _ := q("uid=zoe,ou=people,"+testBaseDN, false); e.attr("cn") == nil || e.attr("cn").Values[0] != "Zoë Ñandú" {
		t.Errorf("cn UTF-8: %+v", e.attr("cn"))
	}

	var er errResp
	if code := call(t, srv, "GET", "/api/entry?dn="+url.QueryEscape("uid=nadie,"+testBaseDN), nil, &er); code != 400 {
		t.Errorf("entrada inexistente: %d", code)
	}
}

type searchResp struct {
	Entries []Entry `json:"entries"`
	Warning string  `json:"warning"`
}

func TestLDAPSearch(t *testing.T) {
	l := startSlapd(t)
	srv := newTestServer(t)
	connectOK(t, srv, alice(l), testAlicePW)
	people := "ou=people," + testBaseDN

	search := func(body map[string]any) searchResp {
		t.Helper()
		var r searchResp
		if code := call(t, srv, "POST", "/api/search", body, &r); code != 200 {
			t.Fatalf("search %v: %d", body, code)
		}
		return r
	}
	searchErr := func(body map[string]any) string {
		t.Helper()
		var e errResp
		if code := call(t, srv, "POST", "/api/search", body, &e); code != 400 {
			t.Fatalf("search %v: esperaba 400, obtuve %d", body, code)
		}
		return e.Error
	}

	// ámbitos
	for scope, want := range map[string]int{"base": 1, "one": 3, "sub": 4, "": 4} {
		if r := search(map[string]any{"base": people, "scope": scope}); len(r.Entries) != want {
			t.Errorf("ámbito %q: %d entradas, esperaba %d", scope, len(r.Entries), want)
		}
	}

	// filtro + atributos pedidos (separados por coma o espacio)
	r := search(map[string]any{"base": testBaseDN, "filter": "(uid=alice)", "attrs": "mail, sn"})
	if len(r.Entries) != 1 || r.Entries[0].DN != testAliceDN {
		t.Fatalf("filtro uid=alice: %+v", r.Entries)
	}
	names := map[string]bool{}
	for _, a := range r.Entries[0].Attrs {
		names[strings.ToLower(a.Name)] = true
	}
	if len(names) != 2 || !names["mail"] || !names["sn"] {
		t.Errorf("atributos devueltos: %v", names)
	}

	// filtro vacío = (objectClass=*)
	if r := search(map[string]any{"base": people, "scope": "one", "filter": ""}); len(r.Entries) != 3 {
		t.Errorf("filtro vacío: %d", len(r.Entries))
	}
	// filtro con comodín
	if r := search(map[string]any{"base": testBaseDN, "filter": "(&(objectClass=inetOrgPerson)(mail=*@example.org))"}); len(r.Entries) != 2 {
		t.Errorf("filtro compuesto: %d", len(r.Entries))
	}

	// errores
	if msg := searchErr(map[string]any{"base": people, "filter": "(uid="}); !strings.Contains(msg, "filtro inválido") {
		t.Errorf("filtro inválido: %q", msg)
	}
	searchErr(map[string]any{"base": "ou=nada," + testBaseDN})

	// límite pedido por el usuario: devuelve lo que haya + aviso
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 10})
	if len(r.Entries) != 10 {
		t.Errorf("limit=10: %d entradas", len(r.Entries))
	}
	if r.Warning == "" {
		t.Error("limit=10 debería devolver aviso de size limit")
	}
	// límite del servidor (sizelimit 550 < 600): resultados parciales + aviso, no error
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 10000})
	if len(r.Entries) == 0 || len(r.Entries) >= bulkCount || r.Warning == "" {
		t.Errorf("límite del servidor: %d entradas, warning=%q", len(r.Entries), r.Warning)
	}
	// dentro del límite del servidor, sin aviso y paginando (500 por página)
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "filter": "(uid=u05*)", "limit": 1000})
	if len(r.Entries) != 100 || r.Warning != "" {
		t.Errorf("uid=u05*: %d entradas, warning=%q", len(r.Entries), r.Warning)
	}

	// paginación en la búsqueda: Zoe solo ve >100 resultados si la búsqueda es paginada
	connectOK(t, srv, Profile{URL: l.URL, BindDN: testZoeDN}, testZoePW)
	r = search(map[string]any{"base": "ou=bulk," + testBaseDN, "scope": "one", "limit": 1000})
	if len(r.Entries) != bulkCount || r.Warning != "" {
		t.Errorf("búsqueda paginada: %d entradas, warning=%q", len(r.Entries), r.Warning)
	}
}
