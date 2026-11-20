package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"ldap-explorer/internal/slapdtest"
)

// TestScreenshots regenerates the images used in docs/USAGE.md from the real UI
// running against the test directory, so they never go stale:
//
//	make screenshots
//
// It only runs when LDAP_EXPLORER_SCREENSHOTS names an output directory.
func TestScreenshots(t *testing.T) {
	out := os.Getenv("LDAP_EXPLORER_SCREENSHOTS")
	if out == "" {
		t.Skip("LDAP_EXPLORER_SCREENSHOTS not set")
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

	// 3. The query builder: no LDAP knowledge needed.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitReady("#sBase"),
		set("#sBase", "ou=people,"+slapdtest.BaseDN), set("#sScope", "one"), set("#sAttrs", "uid, cn, mail, telephoneNumber"))
	waitJS(t, ctx, "attribute suggestions", `document.querySelectorAll('#attrList option').length > 50`)
	setAttr(t, ctx, 0, "mail")
	setOp(t, ctx, 0, "endsWith")
	setVal(t, ctx, 0, "@example.org")
	run(t, ctx, chromedp.Click("#builder .bfoot .badd:first-child"))
	setAttr(t, ctx, 1, "createTimestamp")
	setOp(t, ctx, 1, "withinDays")
	setVal(t, ctx, 1, "30")
	waitJS(t, ctx, "preview", `/createTimestamp>=\d{14}Z/.test(document.getElementById('bFilter').textContent)`)
	shot("builder")

	// 4. Its results, as a list.
	run(t, ctx, chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "result(s)")
	shot("search-list")

	// 5. The same search as a table, sorted.
	run(t, ctx, chromedp.Click("#viewTable"), chromedp.WaitVisible("#sOut table.rtable"),
		chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`),
		chromedp.Click(`#sOut table.rtable thead th:nth-child(2)`))
	shot("search-table")

	// 5b. Recipes and the explorer.
	run(t, ctx, chromedp.Click(`button[data-tab="recipes"]`), chromedp.WaitVisible("#recipe-find-person"),
		set(`#recipe-find-person [data-param="text"]`, "alice"))
	shot("recipes")
	run(t, ctx, chromedp.Click(`button[data-tab="explore"]`), chromedp.WaitVisible("#xBase"),
		set("#xBase", "ou=people,"+slapdtest.BaseDN), set("#xScope", "one"), chromedp.Click("#btnExplore"))
	waitText(t, ctx, "#xSummary", "Looked at")
	shot("explore")

	// 5c. Passwords: the vault, several users and the Passwords panel.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cStore"))
	fillConnection(t, ctx, "alice@lab", l.URL, slapdtest.AliceDN, slapdtest.AlicePW, "vault")
	run(t, ctx, chromedp.Click("#btnSaveProfile"))
	waitJS(t, ctx, "create dialog", `document.getElementById('unlockDlg').open`)
	run(t, ctx, set("#uPass", "correct horse battery"), set("#uConfirm", "correct horse battery"))
	shot("vault-create")
	run(t, ctx, chromedp.Click("#uOk"))
	waitText(t, ctx, "#status", "Profile saved")
	run(t, ctx, chromedp.Click("#btnDupProfile"), set("#cName", "bob@lab"), set("#cBind", slapdtest.BobDN), set("#cPass", slapdtest.BobPW),
		chromedp.Click("#btnSaveProfile"))
	waitJS(t, ctx, "bob saved", `document.querySelectorAll('#quickConn option').length === 3`)
	run(t, ctx, chromedp.Click("#btnCancel"), chromedp.Click("#btnVault"), chromedp.WaitVisible("#mList"))
	waitText(t, ctx, `#mList tr[data-profile="bob@lab"] .state`, "saved")
	shot("passwords")
	run(t, ctx, chromedp.Click("#mClose"))

	// 6. Dark theme (follows the system setting), on the table again.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`))
	run(t, ctx, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
		{Name: "prefers-color-scheme", Value: "dark"}}))
	shot("search-table-dark")
}
