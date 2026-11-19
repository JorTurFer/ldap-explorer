// Package e2e holds the browser tests: a real Chrome drives the real ldapnav
// binary against a real OpenLDAP. It is a separate module so that the app's
// go.mod does not carry the browser-automation dependency.
package e2e
