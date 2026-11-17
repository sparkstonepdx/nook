package core

import (
	"io"
	"slices"
	"strings"
	"testing"
)

func TestPublicPortChecks(t *testing.T) {
	for _, p := range []int{0, 22, 80, 443, 2019, 20005, 70000} {
		if checkPublicPort(p, 20000, 29999) == nil {
			t.Errorf("granted %d", p)
		}
	}
	for _, p := range []int{25, 465, 587, 993, 30001} {
		if err := checkPublicPort(p, 20000, 29999); err != nil {
			t.Errorf("%d: %v", p, err)
		}
	}
	if ps, err := ParsePorts("25,465 587"); err != nil || !slices.Equal(ps, []int{25, 465, 587}) {
		t.Fatal(ps, err)
	}
	if _, err := ParsePorts("25,smtp"); err == nil {
		t.Fatal("parsed a name")
	}
}

func TestOpenPorts(t *testing.T) {
	old := grantedPorts
	defer func() { grantedPorts = old }()
	grantedPorts = func(string) []int { return []int{25, 993} }
	h := &Host{Out: io.Discard}
	taken, missing := map[int]string{}, map[int]bool{}
	if got := h.openPorts("mail", "mail", []int{993, 25, 587}, taken, missing); !slices.Equal(got, []int{25, 993}) {
		t.Fatalf("only asked and granted: %v", got)
	}
	if !missing[587] || len(missing) != 1 {
		t.Fatalf("587 is asked for and not granted: %v", missing)
	}
	if got := h.openPorts("mail", "other", []int{25}, taken, missing); len(got) != 0 {
		t.Fatalf("two sites can't both have 25: %v", got)
	}
}

func TestPublicPortUnitLines(t *testing.T) {
	base := sandbox("/srv/nook/mail", "mail", bindRules(20001, false, 20000, 29999))
	if withPublicPorts(base, nil) != base {
		t.Fatal("no ports, no change")
	}
	low := withPublicPorts(base, []int{25, 993})
	for _, want := range []string{"SocketBindAllow=tcp:25\nSocketBindAllow=tcp:993\nSocketBindDeny=any\n", "AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n"} {
		if !strings.Contains(low, want) {
			t.Fatalf("missing %q in\n%s", want, low)
		}
	}
	if strings.Contains(low, "CapabilityBoundingSet=\n") {
		t.Fatal("still drops every capability")
	}
	high := withPublicPorts(base, []int{30001})
	if strings.Contains(high, "CAP_NET_BIND_SERVICE") || !strings.Contains(high, "CapabilityBoundingSet=\n") {
		t.Fatal("a port over 1024 needs no capability")
	}
	if strings.Index(low, "SocketBindAllow=tcp:20001") > strings.Index(low, "SocketBindDeny=any") {
		t.Fatal("allows go before the deny")
	}
}
