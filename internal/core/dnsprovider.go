package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DNSProvider is a DNS host Caddy can answer the challenge through. Add others here.
type DNSProvider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Module   string   `json:"module"`   // in `caddy list-modules`
	Package  string   `json:"package"`  // its Go package, for a Caddy build from caddyserver.com
	Hint     string   `json:"hint"`     // one line about the token
	TokenURL string   `json:"tokenUrl"` // where tokens are made
	Steps    []string `json:"steps"`    // how to make one; {zone} is the domain's zone
	verify   func(token, domain string) error
}

var dnsProviders = []DNSProvider{
	{ID: "cloudflare", Name: "Cloudflare", Module: "dns.providers.cloudflare", Package: "github.com/caddy-dns/cloudflare",
		Hint:     `A token from the "Edit zone DNS" template, scoped to this domain's zone.`,
		TokenURL: "https://dash.cloudflare.com/profile/api-tokens",
		Steps: []string{
			"Open Cloudflare's API Tokens page (My Profile → API Tokens) and press Create Token.",
			"Next to Edit zone DNS, press Use template. It already has the one permission needed: Zone · DNS · Edit.",
			"Under Zone Resources, keep Include · Specific zone and pick {zone}.",
			"Press Continue to summary, then Create Token.",
			"Copy the token: Cloudflare shows it only once. Paste it below.",
		},
		verify: verifyCloudflare},
}

func dnsProvider(id string) (DNSProvider, bool) {
	for _, p := range dnsProviders {
		if p.ID == id {
			return p, true
		}
	}
	return DNSProvider{}, false
}

var certHTTP = &http.Client{Timeout: 20 * time.Second}

func cfGet(token, path string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", cloudflareAPI+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := certHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Cloudflare: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("Cloudflare answered %s", resp.Status)
	}
	return body, nil
}

// verifyCloudflare checks the token is live and can see the zone the domain is in.
func verifyCloudflare(token, domain string) error {
	if b, err := cfGet(token, "/user/tokens/verify"); err != nil {
		return err
	} else if b["success"] != true {
		// account-owned tokens verify elsewhere; the zone check below still decides
		if _, isAccount := strings.CutPrefix(token, "cfat_"); !isAccount {
			return fmt.Errorf("Cloudflare doesn't accept that token (%s)", cfErrors(b))
		}
	}
	labels := strings.Split(domain, ".")
	for i := 0; i+2 <= len(labels); i++ {
		zone := strings.Join(labels[i:], ".")
		b, err := cfGet(token, "/zones?name="+url.QueryEscape(zone))
		if err != nil {
			return err
		}
		if res, ok := b["result"].([]any); ok && len(res) > 0 {
			return nil
		}
	}
	return fmt.Errorf("the token works but can't see the zone for %s. Make it from the \"Edit zone DNS\" template, scoped to that zone", domain)
}

func cfErrors(b map[string]any) string {
	var msgs []string
	if errs, ok := b["errors"].([]any); ok {
		for _, e := range errs {
			if m, ok := e.(map[string]any); ok {
				msgs = append(msgs, fmt.Sprint(m["message"]))
			}
		}
	}
	if len(msgs) == 0 {
		return "no reason given"
	}
	return strings.Join(msgs, "; ")
}
