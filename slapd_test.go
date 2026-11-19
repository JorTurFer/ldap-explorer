package main

// Starts an ephemeral real OpenLDAP (slapd) for the integration tests.
//
// It looks for slapd/slapadd in LDAPNAV_SLAPD_DIR, in the PATH and in common
// locations. If they are missing, the tests that need it are skipped (t.Skip),
// unless LDAPNAV_REQUIRE_SLAPD=1 (CI): then they fail. A single slapd is
// started per test run and shared (the app is read-only). See the README.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testBaseDN  = "dc=example,dc=org"
	testAdminDN = "cn=admin,dc=example,dc=org"
	testAdminPW = "adminpw"
	testAliceDN = "uid=alice,ou=people,dc=example,dc=org"
	testAlicePW = "alicepw"
	testBobDN   = "uid=bob,ou=people,dc=example,dc=org" // no size limit on the server
	testBobPW   = "bobpw"
	// Zoe can only see more than 100 results if she pages (like AD's MaxPageSize).
	testZoeDN = "uid=zoe,ou=people,dc=example,dc=org"
	testZoePW = "zoepw"
	bulkCount = 600 // more than the page size (500) to test paging
)

type testLDAP struct {
	URL, LDAPSURL string
}

func findBin(name string) string {
	var cands []string
	if d := os.Getenv("LDAPNAV_SLAPD_DIR"); d != "" {
		cands = append(cands, filepath.Join(d, name))
	}
	if p, err := exec.LookPath(name); err == nil {
		cands = append(cands, p)
	}
	for _, d := range []string{"/usr/sbin", "/usr/lib/openldap", "/opt/homebrew/opt/openldap/libexec", "/usr/local/opt/openldap/libexec"} {
		cands = append(cands, filepath.Join(d, name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func firstDir(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func writeSelfSignedCert(dir string) (certPath, keyPath string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return
	}
	err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var (
	slapdOnce   sync.Once
	slapdShared *testLDAP
	slapdSkip   string // reason why there is no slapd (not an error)
	slapdErr    error
	slapdStop   = func() {}
)

// TestMain shuts down the shared slapd when done.
func TestMain(m *testing.M) {
	code := m.Run()
	slapdStop()
	os.Exit(code)
}

// startSlapd returns the shared slapd loaded with testdata/seed.ldif plus
// bulkCount entries in ou=bulk. It listens on ldap:// and ldaps:// (self-signed cert).
func startSlapd(t *testing.T) *testLDAP {
	t.Helper()
	slapdOnce.Do(func() { slapdShared, slapdStop, slapdSkip, slapdErr = launchSlapd() })
	if slapdErr != nil {
		t.Fatalf("could not start slapd: %v", slapdErr)
	}
	if slapdSkip != "" {
		if os.Getenv("LDAPNAV_REQUIRE_SLAPD") == "1" {
			t.Fatalf("LDAPNAV_REQUIRE_SLAPD=1 but: %s", slapdSkip)
		}
		t.Skip(slapdSkip + "; skipping the integration test")
	}
	return slapdShared
}

func launchSlapd() (l *testLDAP, stop func(), skip string, err error) {
	stop = func() {}
	slapd, slapadd := findBin("slapd"), findBin("slapadd")
	if slapd == "" || slapadd == "" {
		return nil, stop, "slapd/slapadd not found (install OpenLDAP or set LDAPNAV_SLAPD_DIR)", nil
	}
	root := filepath.Dir(filepath.Dir(slapd)) // .../usr/sbin/slapd -> .../usr
	schemaDir := firstDir(filepath.Join(root, "etc/ldap/schema"), filepath.Join(root, "../etc/ldap/schema"),
		"/etc/ldap/schema", "/etc/openldap/schema", "/opt/homebrew/etc/openldap/schema", "/usr/local/etc/openldap/schema")
	if schemaDir == "" {
		return nil, stop, "OpenLDAP schema directory not found", nil
	}
	modDir := firstDir(filepath.Join(root, "lib/ldap"), "/usr/lib/ldap", "/usr/lib/openldap", "/usr/lib64/openldap",
		"/opt/homebrew/opt/openldap/libexec/openldap", "/usr/local/opt/openldap/libexec/openldap")

	dir, err := os.MkdirTemp("", "ldapnav-slapd-")
	if err != nil {
		return nil, stop, "", err
	}
	var cmd *exec.Cmd
	stop = func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = os.RemoveAll(dir)
	}
	fail := func(e error) (*testLDAP, func(), string, error) {
		log := ""
		if b, rerr := os.ReadFile(filepath.Join(dir, "slapd.log")); rerr == nil {
			log = "\nslapd log:\n" + string(b)
		}
		stop()
		return nil, func() {}, "", fmt.Errorf("%w%s", e, log)
	}

	certPath, keyPath, err := writeSelfSignedCert(dir)
	if err != nil {
		return fail(err)
	}
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		return fail(err)
	}

	var conf strings.Builder
	for _, s := range []string{"core", "cosine", "inetorgperson"} {
		fmt.Fprintf(&conf, "include %s/%s.schema\n", schemaDir, s)
	}
	if modDir != "" {
		if _, err := os.Stat(filepath.Join(modDir, "back_mdb.so")); err == nil {
			fmt.Fprintf(&conf, "modulepath %s\nmoduleload back_mdb\n", modDir)
		}
	}
	fmt.Fprintf(&conf, `pidfile %s/slapd.pid
sizelimit 550
TLSCertificateFile %s
TLSCertificateKeyFile %s
database mdb
maxsize 104857600
suffix "%s"
rootdn "%s"
rootpw %s
directory %s
index objectClass eq
limits dn.exact="%s" size=unlimited
limits dn.exact="%s" size.soft=100 size.hard=100 size.pr=500 size.prtotal=unlimited
`, dir, certPath, keyPath, testBaseDN, testAdminDN, testAdminPW, data, testBobDN, testZoeDN)
	confPath := filepath.Join(dir, "slapd.conf")
	if err := os.WriteFile(confPath, []byte(conf.String()), 0o600); err != nil {
		return fail(err)
	}

	seed, err := os.ReadFile("testdata/seed.ldif")
	if err != nil {
		return fail(err)
	}
	var ldif strings.Builder
	ldif.Write(seed)
	for i := 1; i <= bulkCount; i++ {
		fmt.Fprintf(&ldif, "\ndn: uid=u%04d,ou=bulk,%s\nobjectClass: inetOrgPerson\nuid: u%04d\ncn: User %04d\nsn: Bulk\n", i, testBaseDN, i, i)
	}
	ldifPath := filepath.Join(dir, "seed.ldif")
	if err := os.WriteFile(ldifPath, []byte(ldif.String()), 0o600); err != nil {
		return fail(err)
	}
	// -q: quick mode (no sync on every entry).
	if out, err := exec.Command(slapadd, "-q", "-f", confPath, "-l", ldifPath).CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Unrecognized database type (mdb)") {
			// e.g. the OpenLDAP bundled with macOS has no mdb backend.
			stop()
			return nil, func() {}, slapd + " does not support the mdb backend (install a full OpenLDAP, e.g. brew install openldap)", nil
		}
		return fail(fmt.Errorf("slapadd: %v\n%s", err, out))
	}

	lp, err := freePort()
	if err != nil {
		return fail(err)
	}
	sp, err := freePort()
	if err != nil {
		return fail(err)
	}
	cmd = exec.Command(slapd, "-f", confPath, "-d", "0",
		"-h", fmt.Sprintf("ldap://127.0.0.1:%d ldaps://127.0.0.1:%d", lp, sp))
	logf, err := os.Create(filepath.Join(dir, "slapd.log"))
	if err != nil {
		return fail(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	for i := 0; i < 100; i++ {
		ok := true
		for _, p := range []int{lp, sp} {
			c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 100*time.Millisecond)
			if e != nil {
				ok = false
				continue
			}
			c.Close()
		}
		if ok {
			return &testLDAP{URL: fmt.Sprintf("ldap://127.0.0.1:%d", lp), LDAPSURL: fmt.Sprintf("ldaps://127.0.0.1:%d", sp)}, stop, "", nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fail(errors.New("slapd did not start in time"))
}
