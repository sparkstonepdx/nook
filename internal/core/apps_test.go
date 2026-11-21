package core

import "testing"

func TestMemBytes(t *testing.T) {
	if memBytes("512M") != 512<<20 || memBytes("2G") != 2<<30 || memBytes("1536M") != 1536<<20 || memBytes("lots") != 0 {
		t.Fatal("memBytes")
	}
}
