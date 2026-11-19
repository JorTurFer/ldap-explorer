// Package slapdtest starts an ephemeral, real OpenLDAP (slapd) for tests.
//
// It is only imported from tests (the app binary never links it).
package slapdtest

//
// The LDAP server is only a validation tool; what is under test is the app
// running on the current OS. slapd can therefore run anywhere reachable on
// 127.0.0.1:
//   - locally: found via LDAPNAV_SLAPD_DIR, PATH or common locations (Linux apt,
//     macOS Homebrew);
//   - in WSL: LDAPNAV_SLAPD_WSL=<distro> runs the distro's slapd (Debian/Ubuntu
//     layout) as a child of the test process. This is how Windows gets a real
//     OpenLDAP (there is no maintained native Windows slapd, and Windows runners
//     only run Windows containers).
//
// If no slapd is available the tests that need it are skipped (t.Skip), unless
// LDAPNAV_REQUIRE_SLAPD=1 (CI): then they fail. A single slapd is started per
// test run and shared (the app is read-only). See the README.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
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

//go:embed seed.ldif
var seedLDIF []byte

const (
	BaseDN  = "dc=example,dc=org"
	AdminDN = "cn=admin,dc=example,dc=org"
	AdminPW = "adminpw"
	AliceDN = "uid=alice,ou=people,dc=example,dc=org"
	AlicePW = "alicepw"
	BobDN   = "uid=bob,ou=people,dc=example,dc=org" // no size limit on the server
	BobPW   = "bobpw"
	// Zoe can only see more than 100 results if she pages (like AD's MaxPageSize).
	ZoeDN     = "uid=zoe,ou=people,dc=example,dc=org"
	ZoePW     = "zoepw"
	BulkCount = 600 // more than the page size (500) to test paging
)

type Server struct {
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

// siblingBin finds a binary next to the chosen slapd (Debian: same dir;
// Homebrew: slapd is in libexec and slapadd in ../sbin). This avoids mixing it
// with another OpenLDAP found in PATH (e.g. the one bundled with macOS).
func siblingBin(slapd, name string) string {
	for _, c := range []string{
		filepath.Join(filepath.Dir(slapd), name),
		filepath.Join(filepath.Dir(slapd), "..", "sbin", name),
	} {
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

func selfSignedCertPEM() (certPEM, keyPEM []byte, err error) {
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
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}

var (
	slapdOnce   sync.Once
	slapdShared *Server
	slapdSkip   string // reason why there is no slapd (not an error)
	slapdErr    error
	slapdStop   = func() {}
)

// Stop shuts the shared slapd down; call it from TestMain after m.Run().
func Stop() { slapdStop() }

// Start returns the shared slapd loaded with seed.ldif plus
// BulkCount entries in ou=bulk. It listens on ldap:// and ldaps:// (self-signed cert).
func Start(t testing.TB) *Server {
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

// slapdHost abstracts where slapd runs: this machine, or a Linux distro in
// WSL. All paths it returns are as seen by slapd itself.
type slapdHost struct {
	wsl string // distro name; empty = local
	dir string // work dir as seen by slapd
}

func (h *slapdHost) command(name string, args ...string) *exec.Cmd {
	if h.wsl == "" {
		return exec.Command(name, args...)
	}
	return exec.Command("wsl.exe", append([]string{"-d", h.wsl, "--", name}, args...)...)
}

func (h *slapdHost) path(rel string) string {
	if h.wsl == "" {
		return filepath.Join(h.dir, rel)
	}
	return h.dir + "/" + rel
}

func (h *slapdHost) writeFile(rel string, data []byte) (string, error) {
	p := h.path(rel)
	if h.wsl == "" {
		return p, os.WriteFile(p, data, 0o600)
	}
	cmd := h.command("tee", p)
	cmd.Stdin = bytes.NewReader(data)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("writing %s in WSL: %v\n%s", p, err, out)
	}
	return p, nil
}

func (h *slapdHost) mkdir(rel string) (string, error) {
	p := h.path(rel)
	if h.wsl == "" {
		return p, os.Mkdir(p, 0o700)
	}
	if out, err := h.command("mkdir", "-p", p).CombinedOutput(); err != nil {
		return "", fmt.Errorf("mkdir %s in WSL: %v\n%s", p, err, out)
	}
	return p, nil
}

func launchSlapd() (l *Server, stop func(), skip string, err error) {
	stop = func() {}
	h := &slapdHost{wsl: os.Getenv("LDAPNAV_SLAPD_WSL")}
	var slapd, slapadd, schemaDir, modDir string
	if h.wsl != "" {
		// Debian/Ubuntu layout inside the distro.
		slapd, slapadd, schemaDir, modDir = "/usr/sbin/slapd", "/usr/sbin/slapadd", "/etc/ldap/schema", "/usr/lib/ldap"
		if out, err := h.command("test", "-x", slapd).CombinedOutput(); err != nil {
			return nil, stop, "slapd not found in WSL distro " + h.wsl + " (apt install slapd): " + strings.TrimSpace(string(out)), nil
		}
	} else {
		slapd = findBin("slapd")
		if slapd != "" {
			slapadd = siblingBin(slapd, "slapadd")
			if slapadd == "" {
				slapadd = findBin("slapadd")
			}
		}
		if slapd == "" || slapadd == "" {
			return nil, stop, "slapd/slapadd not found (install OpenLDAP or set LDAPNAV_SLAPD_DIR / LDAPNAV_SLAPD_WSL)", nil
		}
		root := filepath.Dir(filepath.Dir(slapd)) // .../usr/sbin/slapd -> .../usr
		schemaDir = firstDir(filepath.Join(root, "etc/ldap/schema"), filepath.Join(root, "../etc/ldap/schema"),
			"/etc/ldap/schema", "/etc/openldap/schema", "/opt/homebrew/etc/openldap/schema", "/usr/local/etc/openldap/schema")
		if schemaDir == "" {
			return nil, stop, "OpenLDAP schema directory not found", nil
		}
		modDir = firstDir(filepath.Join(root, "lib/ldap"), "/usr/lib/ldap", "/usr/lib/openldap", "/usr/lib64/openldap",
			"/opt/homebrew/opt/openldap/libexec/openldap", "/usr/local/opt/openldap/libexec/openldap")
		if modDir != "" {
			if _, err := os.Stat(filepath.Join(modDir, "back_mdb.so")); err != nil {
				modDir = "" // backends are built in (e.g. Homebrew)
			}
		}
	}

	// Local dir: slapd log (and, when running locally, the whole work dir).
	localDir, err := os.MkdirTemp("", "ldapnav-slapd-")
	if err != nil {
		return nil, stop, "", err
	}
	if h.wsl == "" {
		h.dir = localDir
	} else {
		out, err := h.command("mktemp", "-d", "/tmp/ldapnav-slapd-XXXXXX").Output()
		if err != nil {
			_ = os.RemoveAll(localDir)
			return nil, stop, "", fmt.Errorf("mktemp in WSL: %w", err)
		}
		h.dir = strings.TrimSpace(string(out))
	}
	var cmd *exec.Cmd
	stop = func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if h.wsl != "" {
			// Killing wsl.exe may leave slapd behind; clean up explicitly.
			_ = h.command("pkill", "-f", h.dir).Run()
			_ = h.command("rm", "-rf", h.dir).Run()
		}
		_ = os.RemoveAll(localDir)
	}
	fail := func(e error) (*Server, func(), string, error) {
		log := ""
		if b, rerr := os.ReadFile(filepath.Join(localDir, "slapd.log")); rerr == nil {
			log = "\nslapd log:\n" + string(b)
		}
		stop()
		return nil, func() {}, "", fmt.Errorf("%w%s", e, log)
	}

	certPEM, keyPEM, err := selfSignedCertPEM()
	if err != nil {
		return fail(err)
	}
	certPath, err := h.writeFile("cert.pem", certPEM)
	if err != nil {
		return fail(err)
	}
	keyPath, err := h.writeFile("key.pem", keyPEM)
	if err != nil {
		return fail(err)
	}
	data, err := h.mkdir("data")
	if err != nil {
		return fail(err)
	}

	var conf strings.Builder
	for _, sc := range []string{"core", "cosine", "inetorgperson", "nis"} {
		fmt.Fprintf(&conf, "include %s/%s.schema\n", schemaDir, sc)
	}
	if modDir != "" {
		fmt.Fprintf(&conf, "modulepath %s\nmoduleload back_mdb\n", modDir)
	}
	fmt.Fprintf(&conf, `pidfile %s
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
`, h.path("slapd.pid"), certPath, keyPath, BaseDN, AdminDN, AdminPW, data, BobDN, ZoeDN)
	confPath, err := h.writeFile("slapd.conf", []byte(conf.String()))
	if err != nil {
		return fail(err)
	}

	var ldif strings.Builder
	ldif.Write(seedLDIF)
	for i := 1; i <= BulkCount; i++ {
		fmt.Fprintf(&ldif, "\ndn: uid=u%04d,ou=bulk,%s\nobjectClass: inetOrgPerson\nuid: u%04d\ncn: User %04d\nsn: Bulk\n", i, BaseDN, i, i)
	}
	ldifPath, err := h.writeFile("seed.ldif", []byte(ldif.String()))
	if err != nil {
		return fail(err)
	}
	// -q: quick mode (no sync on every entry).
	if out, err := h.command(slapadd, "-q", "-f", confPath, "-l", ldifPath).CombinedOutput(); err != nil {
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
	cmd = h.command(slapd, "-f", confPath, "-d", "0",
		"-h", fmt.Sprintf("ldap://127.0.0.1:%d ldaps://127.0.0.1:%d", lp, sp))
	logf, err := os.Create(filepath.Join(localDir, "slapd.log"))
	if err != nil {
		return fail(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	for i := 0; i < 200; i++ {
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
			return &Server{URL: fmt.Sprintf("ldap://127.0.0.1:%d", lp), LDAPSURL: fmt.Sprintf("ldaps://127.0.0.1:%d", sp)}, stop, "", nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fail(errors.New("slapd did not start in time"))
}
