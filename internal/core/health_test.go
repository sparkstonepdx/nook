package core

import "testing"

func TestHealthPath(t *testing.T) {
	for _, ok := range []string{"/healthz", "/api/health", "/v1/health?full=1", "/"} {
		if !healthPathRE.MatchString(ok) {
			t.Errorf("refused %q", ok)
		}
	}
	for _, bad := range []string{"", "healthz", "http://x/health", "/a b", "/x\n", "//evil.example/x\r\nHost: y"} {
		if healthPathRE.MatchString(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}
