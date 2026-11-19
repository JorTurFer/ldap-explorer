package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"ldapnav/internal/slapdtest"
)

// TestScreenshots regenerates the images used in docs/USAGE.md from the real UI
// running against the test directory, so they never go stale:
//
//	make screenshots
//
// It only runs when LDAPNAV_SCREENSHOTS names an output directory.
func TestScreenshots(t *testing.T) {
	out := os.Getenv("LDAPNAV_SCREENSHOTS")
	if out == "" {
		t.Skip("LDAPNAV_SCREENSHOTS not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)

	shot := func(name string) {
		t.Helper()
		var png []byte
		run(t, ctx, chromedp.CaptureScreenshot(&png))
		if err := os.WriteFile(filepath.Join(out, name+".png"), png, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run(t, ctx, chromedp.EmulateViewport(1180, 720), chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// 1. The connection dialog.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cURL"),
		set("#cName", "Example directory"), set("#cURL", l.URL), set("#cBind", slapdtest.AliceDN),
		set("#cPass", slapdtest.AlicePW))
	shot("connect")
	run(t, ctx, chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#status", "Connected to")

	// 2. Browsing: tree expanded, an entry selected.
	root := `.node[title="` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"))
	people := `.node[title="ou=people,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(people), chromedp.Click(people+" .tw"))
	alice := `.node[title="` + slapdtest.AliceDN + `"]`
	run(t, ctx, chromedp.WaitVisible(alice), chromedp.Click(alice))
	waitText(t, ctx, "#pane-entry .dn", "uid=alice,")
	shot("browse")

	// 3. Search, list view.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#sBase"),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"),
		set("#sFilter", "(&(objectClass=inetOrgPerson)(mail=*@example.org))"),
		set("#sAttrs", "uid, cn, mail, telephoneNumber"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "result(s)")
	shot("search-list")

	// 4. The same search as a table, sorted.
	run(t, ctx, chromedp.Click("#viewTable"), chromedp.WaitVisible("#sOut table.rtable"),
		chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`),
		chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`))
	shot("search-table")

	// 5. Dark theme (follows the system setting).
	run(t, ctx, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
		{Name: "prefers-color-scheme", Value: "dark"}}))
	shot("search-table-dark")
}
