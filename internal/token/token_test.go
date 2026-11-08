package token

import "testing"

func TestRoundTrip(t *testing.T) {
	in := Token{V: 1, Host: "1.2.3.4", Port: 22, User: "app-x", Dir: "/srv/apps/x", Domain: "x.com", KnownHosts: "1.2.3.4 ssh-ed25519 AAAA", Key: "-----BEGIN-----\n"}
	out, err := Decode(in.Encode())
	if err != nil || *out != in {
		t.Fatalf("%v %+v", err, out)
	}
	for _, bad := range []string{"", "nope", "apphost1:!!!", "apphost1:e30"} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
