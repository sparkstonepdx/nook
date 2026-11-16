package core

import "testing"

func TestCheckServices(t *testing.T) {
	ok := []Manifest{
		{Kind: "web", Needs: []string{"postgres", "alerts"}},
		{Kind: "service", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"ALERT_URL": "{url}/alert", "ALERT_TOKEN": "{token}"}}}},
		{Kind: "service", Provides: map[string]ProvideSpec{"my-queue": {Env: map[string]string{"QUEUE_URL": "http://{host}:{port}/q?key={token}"}}}},
	}
	for _, m := range ok {
		if err := checkServices(&m); err != nil {
			t.Errorf("%+v: %v", m, err)
		}
	}
	bad := []Manifest{
		{Kind: "web", Needs: []string{"Postgres!"}},
		{Kind: "web", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"ALERT_URL": "x", "ALERT_TOKEN": "y"}}}}, // only services provide
		{Kind: "service", Provides: map[string]ProvideSpec{"alerts": {Env: map[string]string{"HOOK": "{url}"}}}},                  // known interface, wrong names
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"PORT": "{port}"}}}},                      // reserved name
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"X_URL": "a\nExecStart=/bin/sh"}}}},       // newline
		{Kind: "service", Provides: map[string]ProvideSpec{"x": {Env: map[string]string{"X_URL": "$(id)"}}}},                      // shell
	}
	for _, m := range bad {
		if err := checkServices(&m); err == nil {
			t.Errorf("accepted %+v", m)
		}
	}
}

func TestNeedsAndVersions(t *testing.T) {
	for s, want := range map[string]need{"postgres": {"postgres", "", ""}, "postgres>=15": {"postgres", ">=", "15"}, "php@8.3": {"php", "@", "8.3"}} {
		if n, ok := parseNeed(s); !ok || n != want {
			t.Errorf("%s: %+v %v", s, n, ok)
		}
	}
	for _, s := range []string{"Postgres", "pg>15", "php@8.x", "php@", ">=15"} {
		if _, ok := parseNeed(s); ok {
			t.Errorf("accepted need %q", s)
		}
	}
	cases := []struct {
		need, v string
		ok      bool
	}{
		{"postgres>=15", "17", true}, {"postgres>=15", "14", false}, {"postgres>=15", "", false}, {"postgres>=15.2", "15.10", true},
		{"php@8.3", "8.3", true}, {"php@8.3", "8.3.12", true}, {"php@8.3", "8.30", false}, {"php@8.3", "8.4", false},
		{"frankenphp@1", "1.4.2", true}, {"postgres", "", true},
	}
	for _, c := range cases {
		n, _ := parseNeed(c.need)
		if n.accepts(c.v) != c.ok {
			t.Errorf("%s accepts %q: want %v", c.need, c.v, c.ok)
		}
	}
	if versionCmp("8.3", "8.3.0") != 0 || versionCmp("10", "9") != 1 || versionCmp("", "1") != -1 {
		t.Error("versionCmp")
	}
	m := Manifest{Kind: "service", Needs: []string{"postgres>=15"}, Provides: map[string]ProvideSpec{"postgres": {Env: map[string]string{"DATABASE_URL": ""}, Version: "17", Provision: "./provision.sh", Deprovision: "./deprovision.sh"}}}
	if err := checkServices(&m); err != nil {
		t.Error(err)
	}
	for _, bad := range []ProvideSpec{
		{Env: map[string]string{"DATABASE_URL": ""}, Version: "17-beta"},
		{Env: map[string]string{"DATABASE_URL": ""}, Provision: "/bin/sh"},
		{Env: map[string]string{"DATABASE_URL": ""}, Deprovision: "./../x.sh"},
	} {
		m := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"postgres": bad}}
		if checkServices(&m) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestProvisionOutput(t *testing.T) {
	old := runProviderHook
	t.Cleanup(func() { runProviderHook = old })
	p := Provider{Interface: "postgres", spec: ProvideSpec{Provision: "./provision.sh"}}
	runProviderHook = func(app, site, run string, b Binding) (string, error) {
		return "# made role nook-wren\n\nDATABASE_URL=postgresql:///wren?host=/run/postgresql\n", nil
	}
	v, err := provision(p, Binding{App: "wren"})
	if err != nil || v["DATABASE_URL"] != "postgresql:///wren?host=/run/postgresql" {
		t.Fatalf("%v %v", v, err)
	}
	for _, out := range []string{"PATH=/tmp\n", "DATABASE_URL=a\nExecStart=/bin/sh\n", "just text\n"} {
		runProviderHook = func(string, string, string, Binding) (string, error) { return out, nil }
		if _, err := provision(p, Binding{App: "wren"}); err == nil {
			t.Errorf("accepted provision output %q", out)
		}
	}
}
