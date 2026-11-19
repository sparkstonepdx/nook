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
	if Valid(Grant{"a1", "domain", "x.com", ""}) != nil || Valid(Grant{"a1", "wild", "x.com", ""}) == nil || Valid(Grant{"a1", "sub", "x", ""}) == nil {
		t.Error("Valid")
	}
	for _, g := range []Grant{{"mail", "port-domain", "x.com", "25,465,587,993"}, {"mail", "port-host", "mqtt.x.com", "8883"}} {
		if err := Valid(g); err != nil {
			t.Error(err)
		}
	}
	for _, g := range []Grant{{"m", "port-domain", "x.com", ""}, {"m", "port-host", "x.com", "25;rm"}, {"m", "domain", "x.com", "25"}, {"m", "port-host", "x.com", "25,,26"}} {
		if Valid(g) == nil {
			t.Errorf("accepted %+v", g)
		}
	}
}

func TestPortGrants(t *testing.T) {
	l := Parse("mail port-domain client.com 25,465,587,993\nmail port-host mail.client.com 993\nmail host www.mail.com\nblog domain client.com\n")
	if len(l) != 4 || l[0].Ports != "25,465,587,993" || l[0].String() != "mail port-domain client.com 25,465,587,993" || l[2].String() != "mail host www.mail.com" {
		t.Fatalf("%+v", l)
	}
	if l.Allowed("mail", "client.com") || l.Allowed("mail", "mail.client.com") || !l.Allowed("blog", "client.com") {
		t.Error("a port grant isn't a website")
	}
	if d, h := l.PortDomains("mail"), l.PortHosts("mail"); len(d) != 1 || d[0] != "client.com" || len(h) != 1 || h[0] != "mail.client.com" {
		t.Errorf("%v %v", d, h)
	}
	if Parse(l.Text()).Text() != l.Text() {
		t.Error("round trip")
	}
}
