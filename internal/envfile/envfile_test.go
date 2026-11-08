package envfile

import "testing"

func TestRoundTrip(t *testing.T) {
	v := New()
	v.Set("A", `a "quoted" \ value $HOME`)
	v.Set("B", "plain")
	got := Parse(Format(v, "hdr"))
	if s, _ := got.Get("A"); s != `a "quoted" \ value $HOME` {
		t.Fatalf("A=%q", s)
	}
	if got.Len() != 2 {
		t.Fatal("len")
	}
}

func TestParseAndFilter(t *testing.T) {
	v := Filter(Parse("# c\nexport FOO='bar baz'\nPORT=1\n1BAD=x\nX=\"y\"\nnoequals\n"))
	if v.Len() != 2 {
		t.Fatalf("keys %v", v.Keys())
	}
	if s, _ := v.Get("FOO"); s != "bar baz" {
		t.Fatal(s)
	}
	if Check("PATH", "x") == nil || Check("A", "x\ny") == nil || Check("OK", "fine") != nil {
		t.Fatal("Check")
	}
}
