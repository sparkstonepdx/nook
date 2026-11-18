package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/config"
)

func TestListening(t *testing.T) {
	dir := t.TempDir()
	tcp := filepath.Join(dir, "tcp")
	os.WriteFile(tcp, []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:23E6 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1001        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:4E21 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1001        0 2 1 0000000000000000 100 0 0 10 0
   2: 0100007F:23E6 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1002        0 3 1 0000000000000000 20 4 30 10 -1
`), 0o644)
	ls := listening([]string{tcp, filepath.Join(dir, "missing")})
	if len(ls) != 2 || ls[0] != (listener{9190, 1001}) || ls[1] != (listener{20001, 1001}) {
		t.Fatalf("listening sockets only, port from hex: %+v", ls)
	}
}

func TestFirewallListeningRules(t *testing.T) {
	r := firewallRules(config.Settings{PortMin: 20000, PortMax: 29999}, []string{"9142 . 1001", "9200 . 1001"})
	for _, want := range []string{
		"9142 . 1001, 9200 . 1001 }",
		"set listening {\n\t\ttype inet_service\n\t\telements = { 9142, 9200 }",
		"oif \"lo\" tcp dport @listening tcp dport . meta skuid @owner accept\n\t\toif \"lo\" tcp dport @listening drop",
	} {
		if !strings.Contains(r, want) {
			t.Fatalf("no %q in\n%s", want, r)
		}
	}
	if strings.Index(r, "meta skuid { 0") > strings.Index(r, "@listening drop") {
		t.Fatal("root and Caddy are let through first")
	}
}
