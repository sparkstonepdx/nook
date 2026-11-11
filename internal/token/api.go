package token

import "regexp"

// API deploy tokens: "nook_" + the dashboard's address + "/" + 43 base64url characters (32 random bytes),
// like nook_admin.example.com/AbC... The address lets any CI find the server from the secret alone.
// Tokens made before nook 1.6 have no address ("nook_AbC..."); they still work, with the address given
// separately.
var APIRE = regexp.MustCompile(`^nook_(?:([A-Za-z0-9.-]+(?::[0-9]{1,5})?)/)?[A-Za-z0-9_-]{43}$`)

// APIServer returns the dashboard address an API token carries ("" for an older token without one), and
// whether tok is an API token at all.
func APIServer(tok string) (string, bool) {
	m := APIRE.FindStringSubmatch(tok)
	if m == nil {
		return "", false
	}
	return m[1], true
}
