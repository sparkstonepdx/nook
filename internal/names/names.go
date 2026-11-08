// Package names holds the naming rules shared by nook-admin and nook: app names, site folder names and hostnames.
package names

import (
	"regexp"
	"strings"
)

var (
	appRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	labelRE = `[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?`
	siteRE  = regexp.MustCompile(`^(` + labelRE + `\.)*` + labelRE + `$`)
	hostRE  = regexp.MustCompile(`^(` + labelRE + `\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	keyRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	userRE  = regexp.MustCompile(`^[a-z0-9._-]{2,32}$`)
)

// App reports whether s is a valid app name (also the Linux user nook-<name>).
func App(s string) bool { return appRE.MatchString(s) }

// Site reports whether s is a valid site folder name: a subdomain label ("blog") or a hostname ("client.com").
func Site(s string) bool { return len(s) <= 253 && siteRE.MatchString(s) }

// Host reports whether s is a valid fully qualified hostname (at least one dot, alphabetic TLD).
func Host(s string) bool { return len(s) <= 253 && hostRE.MatchString(s) }

// EnvKey reports whether s is a valid environment variable name.
func EnvKey(s string) bool { return keyRE.MatchString(s) }

// DashboardUser reports whether s is a valid dashboard login name.
func DashboardUser(s string) bool { return userRE.MatchString(s) }

// HostOf turns a site folder name into its hostname: no dot = subdomain of the default domain.
func HostOf(site, baseDomain string) string {
	if strings.Contains(site, ".") {
		return site
	}
	return site + "." + baseDomain
}

// ReservedEnv are set by nook for every running site and can't be overridden.
var ReservedEnv = map[string]bool{"PORT": true, "HOST": true, "SITE": true, "SITE_HOST": true, "HOME": true, "USER": true, "PATH": true, "SHELL": true, "LOGNAME": true}

// Preview turns a site and PR number into the preview site name: "myapp" -> "myapp-pr-12", "client.com" -> "pr-12.client.com".
func Preview(site string, pr int) string {
	if strings.Contains(site, ".") {
		return "pr-" + itoa(pr) + "." + strings.TrimPrefix(site, "www.")
	}
	return site + "-pr-" + itoa(pr)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
