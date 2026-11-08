package scrypt

import (
	"encoding/hex"
	"testing"
)

// RFC 7914 section 12 test vectors.
func TestRFC7914(t *testing.T) {
	cases := []struct {
		pw, salt string
		n, r, p  int
		want     string
	}{
		{"", "", 16, 1, 1, "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906"},
		{"password", "NaCl", 1024, 8, 16, "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640"},
	}
	for _, c := range cases {
		got, err := Key([]byte(c.pw), []byte(c.salt), c.n, c.r, c.p, 64)
		if err != nil || hex.EncodeToString(got) != c.want {
			t.Errorf("%q: got %x", c.pw, got)
		}
	}
}

// Same as Node's crypto.scryptSync("correct-horse-battery", "00112233445566778899aabbccddeeff", 64).
func TestMatchesNode(t *testing.T) {
	got, _ := Key([]byte("correct-horse-battery"), []byte("00112233445566778899aabbccddeeff"), 16384, 8, 1, 64)
	if hex.EncodeToString(got) != nodeVector {
		t.Fatalf("got %x", got)
	}
}
