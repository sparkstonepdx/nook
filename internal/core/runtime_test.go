package core

import (
	"strings"
	"testing"
)

func TestRuntimeProvides(t *testing.T) {
	m := Manifest{Kind: "runtime", Provides: map[string]ProvideSpec{
		"php":        {Version: "8.3", Links: map[string]string{"php": "/usr/bin/php8.3"}},
		"frankenphp": {Version: "1.4", Bin: "bin", Env: map[string]string{"FRANKENPHP": "{bin}/frankenphp"}},
	}}
	if err := checkServices(&m); err != nil {
		t.Fatal(err)
	}
	if !m.Provides["php"].Runtime || !m.Provides["frankenphp"].Runtime {
		t.Fatal("a runtime site's provides are runtimes")
	}
	svc := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"php": {Runtime: true, Env: map[string]string{"X": "y"}}}}
	if err := checkServices(&svc); err != nil || svc.Provides["php"].Runtime {
		t.Fatalf("only nook says what's a runtime: %v %+v", err, svc.Provides["php"])
	}
	for _, spec := range []ProvideSpec{
		{Provision: "./p.sh"},
		{Posts: true},
		{Bin: "../x"},
		{Bin: "/usr/bin"},
		{Links: map[string]string{"php": "/etc/shadow"}},
		{Links: map[string]string{"php": "/usr/bin/../../etc/shadow"}},
		{Links: map[string]string{"../php": "/usr/bin/php8.3"}},
	} {
		m := Manifest{Kind: "runtime", Provides: map[string]ProvideSpec{"php": spec}}
		if err := checkServices(&m); err == nil {
			t.Errorf("accepted runtime %+v", spec)
		}
	}
	bad := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"php": {Bin: "bin", Env: map[string]string{"X": "y"}}}}
	if err := checkServices(&bad); err == nil || !strings.Contains(err.Error(), "for runtimes") {
		t.Fatalf("bin on a service: %v", err)
	}
}

func TestRuntimeBin(t *testing.T) {
	if got := runtimeBin("php-8", "php", ProvideSpec{Runtime: true}); got != "/var/lib/nook/runtimes/php-8/php" {
		t.Fatal(got)
	}
	if got := runtimeBin("fp", "fp", ProvideSpec{Runtime: true, Bin: "bin"}); got != "/var/lib/nook/runtimes/fp/fp/bin" {
		t.Fatal(got)
	}
	if runtimeBin("x", "x", ProvideSpec{}) != "" {
		t.Fatal("a service has no bin")
	}
}
