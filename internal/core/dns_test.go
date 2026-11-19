package core

import (
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/grants"
)

func TestParseDNSRecords(t *testing.T) {
	out := `# for client.com
MX  client.com.  10 mail.client.com.
TXT client.com   v=spf1 mx -all
txt stalwart._domainkey.client.com v=DKIM1; k=rsa; p=MIIBIjAN
TXT _dmarc.client.com v=DMARC1; p=quarantine; rua=mailto:dmarc@client.com
SRV _submissions._tcp.client.com 0 1 465 mail.client.com.
TXT other.com v=spf1 -all
A   client.com 1.2.3.4
TXT notclient.com v=spf1 -all
TXT client.com.evil.com x
MX
`
	recs, dropped := parseDNSRecords("stalwart", out, []string{"client.com"})
	if len(recs) != 5 || len(dropped) != 5 {
		t.Fatalf("kept %d, dropped %d: %v", len(recs), len(dropped), dropped)
	}
	if recs[0] != (DNSRecord{"stalwart", "MX", "client.com", "10 mail.client.com."}) || recs[2].Type != "TXT" || recs[2].Value != "v=DKIM1; k=rsa; p=MIIBIjAN" || recs[3].Name != "_dmarc.client.com" {
		t.Fatalf("%+v", recs)
	}
}

func TestAppDNSRecords(t *testing.T) {
	oldH, oldR := dnsHooks, runDNSHook
	t.Cleanup(func() { dnsHooks, runDNSHook = oldH, oldR })
	dnsHooks = func(app string) map[string]string {
		if app == "stalwart" {
			return map[string]string{"mail": "./dns.sh"}
		}
		return map[string]string{"www": "./dns.sh"}
	}
	runDNSHook = func(app, site, run string, domains, hosts []string) (string, error) {
		var b strings.Builder
		for _, d := range domains {
			b.WriteString("MX " + d + " 10 mail." + d + ".\nTXT " + d + " v=spf1 mx -all\n")
		}
		b.WriteString("MX client.com 10 evil.example.\n") // not its domain, unless granted
		return b.String(), nil
	}
	gl := grants.List{{App: "stalwart", Kind: "port-domain", Name: "client.com", Ports: "25"}, {App: "stalwart", Kind: "port-domain", Name: "client.org", Ports: "25"}, {App: "stalwart", Kind: "host", Name: "mail.client.com"}, {App: "blog", Kind: "domain", Name: "blog.com"}}
	recs := appDNSRecords(gl)
	if len(recs) != 5 || recs[0].Name != "client.com" || recs[len(recs)-1].Name != "client.org" {
		t.Fatalf("%+v", recs)
	}
	for _, r := range recs {
		if r.App != "stalwart" {
			t.Fatalf("an app without a ports grant got records: %+v", r)
		}
	}
	if gl.Allowed("stalwart", "client.com") || !gl.Allowed("stalwart", "mail.client.com") || len(gl.PortDomains("stalwart")) != 2 {
		t.Fatal("a ports grant isn't a website")
	}
	if err := grants.Valid(grants.Grant{App: "stalwart", Kind: "port-domain", Name: "client.com", Ports: "25,993"}); err != nil {
		t.Fatal(err)
	}
}
