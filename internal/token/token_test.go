package token

import "testing"

func TestRoundTrip(t *testing.T) {
	in := Token{V: 1, Host: "1.2.3.4", Port: 22, User: "nook-x", Dir: "/srv/nook/x", Domain: "x.com", KnownHosts: "1.2.3.4 ssh-ed25519 AAAA", Key: "-----BEGIN-----\n"}
	out, err := Decode(in.Encode())
	if err != nil || *out != in {
		t.Fatalf("%v %+v", err, out)
	}
	for _, bad := range []string{"", "nope", "nook1:!!!", "nook1:e30"} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestOldPrefix(t *testing.T) {
	in := Token{V: 1, Host: "h", User: "nook-x", Domain: "d", KnownHosts: "k", Key: "key"}
	old := "apphost1:" + in.Encode()[len("nook1:"):]
	if got, err := Decode(old); err != nil || got.User != "nook-x" {
		t.Fatalf("old token: %v %v", got, err)
	}
}

func TestRejectsOptionLikeFields(t *testing.T) {
	for _, tk := range []Token{
		{Host: "-oProxyCommand=x", User: "nook-x", Domain: "d", KnownHosts: "k", Key: "k"},
		{Host: "h", User: "-oProxyCommand=x", Domain: "d", KnownHosts: "k", Key: "k"},
		{Host: "h", User: "root", Domain: "d", KnownHosts: "k", Key: "k"},
	} {
		if _, err := Decode(tk.Encode()); err == nil {
			t.Fatalf("accepted %+v", tk)
		}
	}
}
