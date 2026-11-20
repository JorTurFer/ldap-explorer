module github.com/JorTurFer/ldap-explorer/e2e

go 1.27.4

require (
	github.com/JorTurFer/ldap-explorer v0.0.0
	github.com/chromedp/cdproto v0.160.1
	github.com/chromedp/chromedp v0.24.0
)

require github.com/go-json-experiment/json v0.0.0-20260213210345-44df1a37e875 // indirect

replace github.com/JorTurFer/ldap-explorer => ../
