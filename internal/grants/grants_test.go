package grants

import "testing"

func TestAllowed(t *testing.T) {
	l := Parse("a domain client.com\nb sub client.com\nc host shop.example.org\nd sub base.com\n")
	cases := []struct {
		app, host string
		want      bool
	}{
		{"a", "client.com", true}, {"a", "api.client.com", true}, {"a", "evilclient.com", false},
		{"b", "client.com", false}, {"b", "www.client.com", true}, {"b", "x.y.client.com", true},
		{"c", "shop.example.org", true}, {"c", "x.shop.example.org", false}, {"c", "example.org", false},
		{"d", "blog.base.com", true}, {"d", "base.com", false}, {"a", "blog.base.com", false},
	}
	for _, c := range cases {
		if got := l.Allowed(c.app, c.host); got != c.want {
			t.Errorf("%s %s: got %v", c.app, c.host, got)
		}
	}
	if !l.IsApex("a", "client.com") || l.IsApex("b", "client.com") || !l.IsApex("c", "shop.example.org") {
		t.Error("IsApex")
	}
}

func TestValid(t *testing.T) {
	if Valid(Grant{"a1", "domain", "x.com"}) != nil || Valid(Grant{"a1", "wild", "x.com"}) == nil || Valid(Grant{"a1", "sub", "x"}) == nil {
		t.Error("Valid")
	}
}
