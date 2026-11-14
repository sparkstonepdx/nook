package main

import "testing"

func TestPickPrefix(t *testing.T) {
	rs := []release{{TagName: "registry-v0.2.0"}, {TagName: "push-v0.3.0", Draft: true}, {TagName: "push-v0.2.0-rc1", Prerelease: true}, {TagName: "push-v0.1.0"}}
	if r, ok := pickPrefix(rs, "push-v"); !ok || r.TagName != "push-v0.1.0" {
		t.Fatalf("got %q %v", r.TagName, ok)
	}
	if _, ok := pickPrefix(rs, "other-v"); ok {
		t.Fatal("matched a prefix nothing has")
	}
}
