package core

import (
	"strings"
	"testing"
)

func TestInstructionsNoToken(t *testing.T) {
	full := Instructions("blog", "nook_secret", "")
	if !strings.Contains(full, "nook_secret") || !strings.Contains(full, "1. Deploy token") {
		t.Fatal("full instructions lost the token section")
	}
	rest := InstructionsNoToken("blog", "")
	if strings.Contains(rest, "Deploy token") || strings.Contains(rest, "nook_") || strings.Contains(rest, "nook:") {
		t.Fatalf("token section left in:\n%s", rest)
	}
	if !strings.Contains(rest, "1. Workflow") || !strings.Contains(rest, "4. Shared services") {
		t.Fatalf("sections not renumbered:\n%s", rest)
	}
}
