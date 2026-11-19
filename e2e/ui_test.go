package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"ldapnav/internal/slapdtest"
)

// ---------- helpers ----------

func run(t *testing.T, ctx context.Context, actions ...chromedp.Action) {
	t.Helper()
	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatal(err)
	}
}

// waitJS waits until the JavaScript expression is truthy.
func waitJS(t *testing.T, ctx context.Context, what, expr string) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Poll(expr, nil, chromedp.WithPollingTimeout(20*time.Second))); err != nil {
		t.Fatalf("timed out waiting for %s: %v", what, err)
	}
}

func js(t *testing.T, ctx context.Context, expr string, out any) {
	t.Helper()
	run(t, ctx, chromedp.Evaluate(expr, out))
}

// set sets an input/select value from JS (chromedp.SetValue fails on empty strings).
func set(sel, val string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(`(() => { const e = document.querySelector(%s); e.value = %s; return true; })()`, q(sel), q(val)), nil)
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

// waitText waits until the element matching sel contains text.
func waitText(t *testing.T, ctx context.Context, sel, text string) {
	t.Helper()
	waitJS(t, ctx, fmt.Sprintf("%q to contain %q", sel, text),
		fmt.Sprintf(`!!document.querySelector(%s) && document.querySelector(%s).textContent.includes(%s)`, q(sel), q(sel), q(text)))
}

func textOf(t *testing.T, ctx context.Context, sel string) string {
	t.Helper()
	var s string
	js(t, ctx, fmt.Sprintf(`(document.querySelector(%s)||{textContent:''}).textContent`, q(sel)), &s)
	return s
}

// attrValue returns the text of the value cell of an attribute in the entry pane.
func attrValue(t *testing.T, ctx context.Context, name string) (text string, found bool) {
	t.Helper()
	var r struct {
		Found bool   `json:"found"`
		Text  string `json:"text"`
	}
	js(t, ctx, fmt.Sprintf(`(() => { for (const tr of document.querySelectorAll('#pane-entry table.attrs tr')) {
		if (tr.querySelector('td.k').textContent === %s) return {found:true, text: tr.querySelector('td.v').textContent}; }
		return {found:false, text:''}; })()`, q(name)), &r)
	return r.Text, r.Found
}

func connect(t *testing.T, ctx context.Context, url, bindDN, password string) {
	t.Helper()
	run(t, ctx,
		chromedp.Click("#btnConn"),
		chromedp.WaitVisible("#cURL"),
		set("#cURL", url),
		set("#cBind", bindDN),
		set("#cPass", password),
		chromedp.Click("#btnDoConn"),
	)
}

// ---------- tests ----------

func TestUIConnectBrowseAndSearch(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	if got := textOf(t, ctx, "#status"); got != "Not connected" {
		t.Fatalf("initial status: %q", got)
	}

	connect(t, ctx, l.URL, slapdtest.AliceDN, slapdtest.AlicePW)
	waitText(t, ctx, "#status", "Connected to "+l.URL+" as "+slapdtest.AliceDN)

	// The first root is detected from the RootDSE and selected automatically.
	waitText(t, ctx, "#pane-entry .dn", slapdtest.BaseDN)

	// The root is selected but collapsed: expand it, then ou=people, and open alice.
	root := `.node[title="` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(root), chromedp.Click(root+" .tw"))
	people := `.node[title="ou=people,` + slapdtest.BaseDN + `"]`
	run(t, ctx, chromedp.WaitVisible(people), chromedp.Click(people+" .tw"))
	alice := `.node[title="` + slapdtest.AliceDN + `"]`
	run(t, ctx, chromedp.WaitVisible(alice), chromedp.Click(alice))
	waitText(t, ctx, "#pane-entry .dn", slapdtest.AliceDN)

	if mail, ok := attrValue(t, ctx, "mail"); !ok || mail != "alice@example.org" {
		t.Errorf("mail: %q (found=%v)", mail, ok)
	}
	if photo, ok := attrValue(t, ctx, "jpegPhoto"); !ok || !strings.Contains(photo, "(binary,") {
		t.Errorf("jpegPhoto should be summarised as binary, got %q (found=%v)", photo, ok)
	}
	var first string
	js(t, ctx, `document.querySelector('#pane-entry table.attrs tr td.k').textContent`, &first)
	if first != "objectClass" {
		t.Errorf("objectClass should be the first attribute, got %q", first)
	}

	// Operational attributes toggle.
	if _, ok := attrValue(t, ctx, "entryUUID"); ok {
		t.Error("entryUUID shown without the operational toggle")
	}
	run(t, ctx, chromedp.Click("#chkOp"))
	waitJS(t, ctx, "entryUUID", `[...document.querySelectorAll('#pane-entry table.attrs td.k')].some(td=>td.textContent==='entryUUID')`)

	// Search: filter and attribute list.
	run(t, ctx, chromedp.Click(`button[data-tab="search"]`), chromedp.WaitVisible("#sBase"))
	var base string
	js(t, ctx, `document.getElementById('sBase').value`, &base)
	if base != slapdtest.BaseDN {
		t.Errorf("search base should default to the first root, got %q", base)
	}
	run(t, ctx,
		set("#sFilter", "(uid=alice)"),
		set("#sAttrs", "mail, sn"),
		chromedp.Click("#btnSearch"),
	)
	waitText(t, ctx, "#sOut", "1 result(s)")
	waitText(t, ctx, "#sOut", slapdtest.AliceDN)
	var cells int
	js(t, ctx, `document.querySelectorAll('#sOut table.attrs tr').length`, &cells)
	if cells != 2 {
		t.Errorf("requested 2 attributes, result shows %d rows", cells)
	}

	// A subtree search returning several entries, with Enter to submit.
	run(t, ctx,
		set("#sFilter", "(objectClass=inetOrgPerson)"),
		set("#sAttrs", "uid"),
		set("#sLimit", "10"),
	)
	run(t, ctx, chromedp.SendKeys("#sFilter", "\r"))
	waitText(t, ctx, "#sOut", "10 result(s)")
	waitText(t, ctx, "#sOut", "Size Limit Exceeded") // 10 < number of matches: warning, not an error

	// Invalid filter: rejected with a readable message.
	run(t, ctx, set("#sFilter", "(uid="), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut .err", "invalid filter")

	// "view" on a result opens it in the Entry tab.
	run(t, ctx, set("#sFilter", "(uid=bob)"), chromedp.Click("#btnSearch"))
	waitText(t, ctx, "#sOut", "1 result(s)")
	run(t, ctx, chromedp.Click("#sOut summary a.jump"))
	waitText(t, ctx, "#pane-entry .dn", "uid=bob,")

	// "search from here" sets the base and switches to the Search tab.
	run(t, ctx, chromedp.Click("#pane-entry a.jump"))
	waitJS(t, ctx, "base = bob", `document.getElementById('sBase').value.startsWith('uid=bob,')`)

	// Disconnect.
	run(t, ctx, chromedp.Click("#btnDisc"))
	waitText(t, ctx, "#status", "Not connected")
	waitText(t, ctx, "#tree", "Not connected.")
}

func TestUIWrongPassword(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	connect(t, ctx, l.URL, slapdtest.AliceDN, "definitely-wrong")
	waitText(t, ctx, "#cErr", "bind")
	var dialogOpen bool
	js(t, ctx, `document.getElementById('dlg').open`, &dialogOpen)
	if !dialogOpen {
		t.Error("the dialog should stay open after a failed connection")
	}
	if got := textOf(t, ctx, "#status"); got != "Not connected" {
		t.Errorf("status after a failed connect: %q", got)
	}

	// A bind DN without a password is refused by the client with a clear message.
	run(t, ctx, set("#cPass", ""), chromedp.Click("#btnDoConn"))
	waitText(t, ctx, "#cErr", "empty password")
}

func TestUIAnonymousAndNoBaseDetection(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	connect(t, ctx, l.URL, "", "")
	waitText(t, ctx, "#status", "(anonymous)")
	waitText(t, ctx, "#tree", slapdtest.BaseDN)
}

func TestUIProfiles(t *testing.T) {
	l := slapdtest.Start(t)
	appURL := startApp(t)
	ctx := browser(t)
	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn"))

	// Saving without a name is refused.
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cURL"),
		set("#cURL", l.URL), chromedp.Click("#btnSaveProfile"))
	waitText(t, ctx, "#cErr", "Give the profile a name")

	// Save, then reload the page: the profile is still there and fills the form.
	run(t, ctx,
		set("#cName", "lab"),
		set("#cBind", slapdtest.AliceDN),
		set("#cBase", "ou=people,"+slapdtest.BaseDN),
		set("#cPass", "must-not-be-saved"),
		chromedp.Click("#btnSaveProfile"),
	)
	waitText(t, ctx, "#status", "Profile saved")

	run(t, ctx, chromedp.Navigate(appURL), chromedp.WaitVisible("#btnConn")) // fresh page, same server
	run(t, ctx, chromedp.Click("#btnConn"), chromedp.WaitVisible("#cProfile"))
	waitJS(t, ctx, "profile option", `[...document.querySelectorAll('#cProfile option')].some(o=>o.value==='lab')`)
	run(t, ctx, set("#cProfile", "lab"))
	// SetValue does not fire "change" the way a user selection does.
	run(t, ctx, chromedp.Evaluate(`document.getElementById('cProfile').dispatchEvent(new Event('change'))`, nil))
	waitJS(t, ctx, "form filled", `document.getElementById('cURL').value === `+q(l.URL))
	var bind, pass string
	js(t, ctx, `document.getElementById('cBind').value`, &bind)
	js(t, ctx, `document.getElementById('cPass').value`, &pass)
	if bind != slapdtest.AliceDN || pass != "" {
		t.Errorf("form after loading the profile: bind=%q password=%q", bind, pass)
	}

	// Delete it.
	run(t, ctx, chromedp.Click("#btnDelProfile"))
	waitJS(t, ctx, "profile gone", `![...document.querySelectorAll('#cProfile option')].some(o=>o.value==='lab')`)
}
