package web

import (
	"net/http/httptest"
	"testing"
)

func TestRequestToken(t *testing.T) {
	const tok = "nook:admin.example.com/abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if got, ok := requestToken(r, false); !ok || got != tok {
		t.Errorf("bearer: %q %v", got, ok)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("anyone", tok)
	if _, ok := requestToken(r, false); ok {
		t.Error("basic auth must only count for the token check")
	}
	if got, ok := requestToken(r, true); !ok || got != tok {
		t.Errorf("basic: %q %v", got, ok)
	}
	r.SetBasicAuth("anyone", "not-a-token")
	if _, ok := requestToken(r, true); ok {
		t.Error("a malformed password passed")
	}
}
