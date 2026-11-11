package names

import "testing"

func TestRules(t *testing.T) {
	cases := []struct {
		fn   func(string) bool
		in   string
		want bool
	}{
		{App, "myapp", true}, {App, "a", false}, {App, "My", false}, {App, "1app", false}, {App, "a-b-c", true},
		{Site, "blog", true}, {Site, "client.com", true}, {Site, "pr-12.client.com", true},
		{Site, "..", false}, {Site, "a..b", false}, {Site, ".hidden", false}, {Site, "a/b", false}, {Site, "-x", false}, {Site, "UP", false},
		{Host, "client.com", true}, {Host, "blog", false}, {Host, "x.123", false}, {Host, "a.b.co", true},
		{EnvKey, "API_KEY", true}, {EnvKey, "1X", false}, {EnvKey, "A-B", false},
		{DashboardUser, "adam", true}, {DashboardUser, "a", false}, {DashboardUser, "a b", false},
	}
	for _, c := range cases {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestHostOfAndPreview(t *testing.T) {
	if HostOf("blog", "x.com") != "blog.x.com" || HostOf("client.com", "x.com") != "client.com" {
		t.Fatal("HostOf")
	}
	if Preview("myapp", 12) != "myapp-pr-12" || Preview("www.client.com", 3) != "pr-3.client.com" || Preview("client.com", 3) != "pr-3.client.com" {
		t.Fatal("Preview")
	}
}

func TestPreviewOf(t *testing.T) {
	for _, c := range []struct {
		name, base string
		pr         int
		ok         bool
	}{
		{"myapp-pr-12", "myapp", 12, true},
		{"pr-3.client.com", "client.com", 3, true},
		{"client.com", "", 0, false},
		{"my-pr-app", "", 0, false},
		{"pr-x.client.com", "", 0, false},
		{"a-pr-0", "", 0, false},
	} {
		b, n, ok := PreviewOf(c.name)
		if b != c.base || n != c.pr || ok != c.ok {
			t.Errorf("PreviewOf(%q) = %q %d %v", c.name, b, n, ok)
		}
		if ok && Preview(b, n) != c.name {
			t.Errorf("round trip %q", c.name)
		}
	}
}
