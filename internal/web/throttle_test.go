package web

import (
	"fmt"
	"testing"
)

func TestThrottlePerClient(t *testing.T) {
	s := &Server{}
	for i := 0; i < 6; i++ {
		if err := s.throttled("1.2.3.4"); err != nil {
			t.Fatalf("try %d throttled early: %v", i, err)
		}
		s.failed("1.2.3.4")
	}
	if s.throttled("1.2.3.4") == nil {
		t.Fatal("not throttled after 6 failures")
	}
	if s.throttled("5.6.7.8") != nil {
		t.Fatal("another client was throttled")
	}
}

func TestThrottleIPv6Slash64(t *testing.T) {
	s := &Server{}
	for i := 0; i < 6; i++ {
		ip := fmt.Sprintf("2001:db8:1:2::%x", i+1)
		if err := s.throttled(ip); err != nil {
			t.Fatalf("try %d: %v", i, err)
		}
		s.failed(ip)
	}
	if s.throttled("2001:db8:1:2:ffff::9") == nil {
		t.Fatal("a fresh address in the same /64 wasn't throttled")
	}
}

func TestThrottleGlobal(t *testing.T) {
	s := &Server{}
	s.succeeded("9.9.9.9")
	for i := 0; i < globalMax; i++ {
		s.failed(fmt.Sprintf("10.0.%d.%d", i/200, i%200))
	}
	if s.throttled("203.0.113.77") == nil {
		t.Fatal("new client not paused after the global limit")
	}
	if err := s.throttled("9.9.9.9"); err != nil {
		t.Fatalf("known-good client paused: %v", err)
	}
}
