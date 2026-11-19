package e2e

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/log"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"ldap-explorer/internal/slapdtest"
)

var binPath string

// TestMain builds the real ldap-explorer binary once and stops the shared slapd at the end.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ldap-explorer-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "ldap-explorer")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = ".."
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building ldap-explorer: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	slapdtest.Stop()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var urlRe = regexp.MustCompile(`http://127\.0\.0\.1:\d+/#[0-9a-f]+`)

// startApp runs the real binary with an isolated profiles directory and
// returns its URL (including the #token).
func startApp(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	cmd := exec.Command(binPath, "-no-open", "-idle-exit", "0")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "HOME="+cfg, "AppData="+cfg)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := urlRe.FindString(sc.Text()); m != "" {
				found <- m
				break
			}
		}
		for sc.Scan() { // keep draining
		}
	}()
	select {
	case u := <-found:
		return u
	case <-time.After(15 * time.Second):
		t.Fatal("ldap-explorer did not print its URL")
		return ""
	}
}

var chromeOnce sync.Once
var chromeErr error

// browser starts a headless Chrome tab. Any JavaScript exception, console error
// or CSP violation fails the test. The test is skipped if Chrome is missing,
// unless LDAP_EXPLORER_REQUIRE_CHROME=1 (CI).
func browser(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		opts = append(opts, chromedp.NoSandbox) // containers run as root
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(alloc)
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(func() { cancelTimeout(); cancelCtx(); cancelAlloc() })

	if err := chromedp.Run(ctx); err != nil {
		if os.Getenv("LDAP_EXPLORER_REQUIRE_CHROME") == "1" {
			t.Fatalf("LDAP_EXPLORER_REQUIRE_CHROME=1 but Chrome could not start: %v", err)
		}
		t.Skipf("Chrome not available: %v", err)
	}

	var mu sync.Mutex
	var problems []string
	add := func(s string) { mu.Lock(); problems = append(problems, s); mu.Unlock() }
	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *cdpruntime.EventExceptionThrown:
			add("exception: " + e.ExceptionDetails.Error())
		case *cdpruntime.EventConsoleAPICalled:
			if e.Type == cdpruntime.APITypeError {
				s := "console.error:"
				for _, a := range e.Args {
					s += " " + string(a.Value)
				}
				add(s)
			}
		case *log.EventEntryAdded:
			// The browser logs every failed HTTP response. The API answers 400 on
			// purpose for user errors (bad password, invalid filter...): not a bug.
			expected := e.Entry.Source == log.SourceNetwork && strings.Contains(e.Entry.URL, "/api/") &&
				(strings.Contains(e.Entry.Text, "status of 400") || // user errors: bad password, invalid filter...
					strings.Contains(e.Entry.Text, "status of 423") || // the vault is locked: the UI asks for the passphrase and retries
					strings.Contains(e.Entry.Text, "status of 429")) // unlock attempts throttled
			if e.Entry.Level == log.LevelError && !expected {
				add("browser log: " + e.Entry.Text + " " + e.Entry.URL)
			}
		}
	})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range problems {
			t.Errorf("browser problem: %s", p)
		}
	})
	return ctx
}
