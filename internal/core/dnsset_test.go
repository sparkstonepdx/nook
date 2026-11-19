package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/grants"
)

func TestSetDNS(t *testing.T) {
	dir := t.TempDir()
	certsFile, dnsEnvFile = filepath.Join(dir, "certs.json"), filepath.Join(dir, "dns.env")
	os.WriteFile(certsFile, []byte(`{"wildcards":[{"domain":"client.com","provider":"cloudflare","env":"NOOK_DNS_1"}]}`), 0o600)
	os.WriteFile(dnsEnvFile, []byte("NOOK_DNS_1=cf-token\n"), 0o600)
	oldH, oldR, oldCF, oldG := dnsHooks, runDNSHook, cfDo, loadGrants
	t.Cleanup(func() { dnsHooks, runDNSHook, cfDo, loadGrants = oldH, oldR, oldCF, oldG })
	loadGrants = func() grants.List {
		return grants.Parse("stalwart port-domain client.com 25,993\nstalwart host mail.client.com\n")
	}
	dnsHooks = func(string) map[string]string { return map[string]string{"mail": "./dns.sh"} }
	runDNSHook = func(app, site, run string, domains, hosts []string) (string, error) {
		return "MX client.com 10 mail.client.com.\nTXT client.com v=spf1 mx -all\nTXT _dmarc.client.com v=DMARC1; p=quarantine\nTXT s1._domainkey.client.com v=DKIM1; k=rsa; p=ABC\n", nil
	}
	// the zone as it is: Google's MX, an old SPF, a site verification TXT, the DMARC already right
	zone := map[string][]map[string]any{
		"MX client.com":                {{"id": "mx1", "type": "MX", "priority": float64(1), "content": "aspmx.l.google.com"}, {"id": "mx2", "type": "MX", "priority": float64(5), "content": "alt1.aspmx.l.google.com"}},
		"TXT client.com":               {{"id": "t1", "type": "TXT", "content": `"v=spf1 include:_spf.google.com ~all"`}, {"id": "t2", "type": "TXT", "content": "google-site-verification=xyz"}},
		"TXT _dmarc.client.com":        {{"id": "d1", "type": "TXT", "content": "v=DMARC1; p=quarantine"}},
		"TXT s1._domainkey.client.com": nil,
	}
	var calls []string
	cfDo = func(token, method, path string, body any) (map[string]any, error) {
		if token != "cf-token" {
			return nil, fmt.Errorf("wrong token")
		}
		calls = append(calls, method+" "+path)
		switch {
		case strings.HasPrefix(path, "/zones?name="):
			if strings.HasSuffix(path, "=client.com") {
				return map[string]any{"result": []any{map[string]any{"id": "Z1"}}}, nil
			}
			return map[string]any{"result": []any{}}, nil
		case method == "GET":
			q := path[strings.Index(path, "type="):]
			typ := strings.TrimPrefix(strings.Split(q, "&")[0], "type=")
			name := strings.TrimPrefix(strings.Split(q, "&")[1], "name=")
			var res []any
			for _, r := range zone[typ+" "+name] {
				res = append(res, r)
			}
			return map[string]any{"result": res}, nil
		}
		return map[string]any{"success": true}, nil
	}
	plan, err := PlanDNS("stalwart", "client.com")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DNSChange{}
	for _, c := range plan.Changes {
		got[c.Type+" "+c.Name+" "+txtKind(c.Value)] = c
	}
	if c := got["MX client.com "]; c.Action != "replace" || len(c.Old) != 2 || c.Old[0] != "1 aspmx.l.google.com" {
		t.Fatalf("MX: %+v", c)
	}
	if c := got["TXT client.com v=spf1"]; c.Action != "replace" || len(c.Old) != 1 || !strings.Contains(c.Old[0], "_spf.google.com") {
		t.Fatalf("SPF replaces only the old SPF, not the verification: %+v", c)
	}
	if got["TXT _dmarc.client.com v=DMARC1"].Action != "ok" || got["TXT s1._domainkey.client.com v=DKIM1"].Action != "add" {
		t.Fatalf("%+v", plan.Changes)
	}
	calls = nil
	if err := New(&strings.Builder{}).SetDNS("stalwart", "client.com"); err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, c := range calls {
		if !strings.HasPrefix(c, "GET") {
			writes = append(writes, c)
		}
	}
	want := []string{"DELETE /zones/Z1/dns_records/mx1", "DELETE /zones/Z1/dns_records/mx2", "POST /zones/Z1/dns_records", "DELETE /zones/Z1/dns_records/t1", "POST /zones/Z1/dns_records", "POST /zones/Z1/dns_records"}
	if strings.Join(writes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes:\n%s", strings.Join(writes, "\n"))
	}
	if r, _ := cfRecord("SRV", "_submissions._tcp.client.com", "0 1 465 mail.client.com."); r["data"].(map[string]any)["port"] != 465 {
		t.Fatalf("SRV: %v", r)
	}
	if _, err := PlanDNS("other", "client.com"); err == nil {
		t.Fatal("an app without a port grant there got a plan")
	}
}
