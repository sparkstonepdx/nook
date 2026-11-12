package token

import "regexp"

// API deploy tokens: "nook:" + the dashboard's address + "/" + 43 base64url characters (32 random bytes),
// like nook:admin.example.com/AbC... The address lets any CI find the server from the secret alone.
// Still accepted: nook 1.6's "nook_admin.example.com/AbC...", and tokens from before it with no address
// ("nook_AbC..."), which need the address given separately.
var APIRE = regexp.MustCompile(`^nook(?::([A-Za-z0-9.-]+(?::[0-9]{1,5})?)/|_(?:([A-Za-z0-9.-]+(?::[0-9]{1,5})?)/)?)[A-Za-z0-9_-]{43}$`)

// IsAPI reports whether s looks like an API token (any of its forms), as opposed to an old SSH token.
func IsAPI(s string) bool { return APIRE.MatchString(s) }

// APIServer returns the dashboard address an API token carries ("" for an older token without one), and
// whether tok is an API token at all.
func APIServer(tok string) (string, bool) {
	m := APIRE.FindStringSubmatch(tok)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}
