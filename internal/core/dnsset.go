package core

// Setting a mail domain's records through Cloudflare (#95 step 2), with the token already saved for the domain's
// wildcard certificate (an "Edit zone DNS" token). The dashboard shows the plan first: what's already right, what's
// added, and what it replaces, since replacing an MX moves where the domain's mail goes.
//
// What it replaces, at the same name: every MX (a domain's mail goes to one place), a CNAME or SRV, and only the
// TXT record that starts the same way (v=spf1, v=DMARC1, v=DKIM1). Other TXT records, like a site verification,
// are left alone.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
)

type DNSChange struct {
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Value  string   `json:"value"`
	Action string   `json:"action"` // ok, add, replace
	Old    []string `json:"old,omitempty"`
	oldIDs []string
}

type DNSPlan struct {
	Domain  string      `json:"domain"`
	App     string      `json:"app"`
	Zone    string      `json:"zone"`
	Changes []DNSChange `json:"changes"`
	zoneID  string
	token   string
}

// cfDo is one Cloudflare API call; a var for tests.
var cfDo = func(token, method, path string, body any) (map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, cloudflareAPI+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := certHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Cloudflare: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("Cloudflare answered %s", resp.Status)
	}
	if out["success"] != true {
		return out, fmt.Errorf("Cloudflare: %s", cfErrors(out))
	}
	return out, nil
}

// cfZone finds the zone holding domain with one of the saved Cloudflare tokens.
func cfZone(domain string) (token, zoneID, zone string, err error) {
	env := readDNSEnv()
	tried := map[string]bool{}
	for _, w := range loadCerts().Wildcards {
		tok := env[w.Env]
		if w.Provider != "cloudflare" || tok == "" || tried[tok] {
			continue
		}
		tried[tok] = true
		labels := strings.Split(domain, ".")
		for i := 0; i+2 <= len(labels); i++ {
			name := strings.Join(labels[i:], ".")
			b, err := cfDo(tok, "GET", "/zones?name="+url.QueryEscape(name), nil)
			if err != nil {
				continue
			}
			if res, ok := b["result"].([]any); ok && len(res) > 0 {
				if z, ok := res[0].(map[string]any); ok {
					return tok, fmt.Sprint(z["id"]), name, nil
				}
			}
		}
	}
	return "", "", "", fmt.Errorf("no saved Cloudflare token can see %s. Set up a wildcard certificate for it with Cloudflare first (Domains → Certificates); that token sets these records too", domain)
}

// HasCloudflareToken says whether some saved token is for Cloudflare (the page offers to set records then).
func HasCloudflareToken() bool {
	env := readDNSEnv()
	for _, w := range loadCerts().Wildcards {
		if w.Provider == "cloudflare" && env[w.Env] != "" {
			return true
		}
	}
	return false
}

// cfRecord is what Cloudflare stores for a record: its content (and priority or SRV data).
func cfRecord(typ, name, value string) (map[string]any, error) {
	rec := map[string]any{"type": typ, "name": name, "ttl": 1}
	f := strings.Fields(value)
	switch typ {
	case "MX":
		if len(f) != 2 {
			return nil, fmt.Errorf("MX %s: want \"PRIORITY HOST\", not %q", name, value)
		}
		p, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("MX %s: %q isn't a priority", name, f[0])
		}
		rec["priority"], rec["content"] = p, strings.TrimSuffix(f[1], ".")
	case "SRV":
		if len(f) != 4 {
			return nil, fmt.Errorf("SRV %s: want \"PRIORITY WEIGHT PORT TARGET\", not %q", name, value)
		}
		var n [3]int
		for i := range n {
			v, err := strconv.Atoi(f[i])
			if err != nil {
				return nil, fmt.Errorf("SRV %s: %q isn't a number", name, f[i])
			}
			n[i] = v
		}
		rec["data"] = map[string]any{"priority": n[0], "weight": n[1], "port": n[2], "target": strings.TrimSuffix(f[3], ".")}
	case "CNAME":
		rec["content"] = strings.TrimSuffix(value, ".")
	default:
		rec["content"] = value
	}
	return rec, nil
}

// cfShown is an existing record as the plan shows it, in the same form the app prints.
func cfShown(r map[string]any) string {
	switch r["type"] {
	case "MX":
		return fmt.Sprintf("%v %v", num(r["priority"]), r["content"])
	case "SRV":
		if d, ok := r["data"].(map[string]any); ok {
			return fmt.Sprintf("%v %v %v %v", num(d["priority"]), num(d["weight"]), num(d["port"]), d["target"])
		}
	}
	return strings.Trim(fmt.Sprint(r["content"]), `"`)
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func sameRecord(want map[string]any, have map[string]any) bool {
	wantRec := map[string]any{"type": want["type"], "priority": want["priority"], "content": want["content"], "data": want["data"]}
	return cfShown(wantRec) == cfShown(have)
}

// txtKind is what a TXT record is, by how it starts: v=spf1, v=DMARC1, v=DKIM1, or "" for anything else.
func txtKind(v string) string {
	v = strings.Trim(strings.TrimSpace(v), `"`)
	for _, k := range []string{"v=spf1", "v=DMARC1", "v=DKIM1"} {
		if strings.HasPrefix(strings.ToLower(v), strings.ToLower(k)) {
			return k
		}
	}
	return ""
}

// loadGrants is a var for tests.
var loadGrants = func() grants.List {
	gl, _ := grants.Load(config.GrantsFile)
	return gl
}

// PlanMailDNS is what setting domain's records would do, without changing anything.
func PlanMailDNS(domain string) (DNSPlan, error) {
	app := loadGrants().MailOwner(domain)
	if app == "" {
		return DNSPlan{}, fmt.Errorf("no app handles mail for %s", domain)
	}
	recs := appDNSRecords(grants.List{{App: app, Kind: "mail", Name: domain}})
	if len(recs) == 0 {
		return DNSPlan{}, fmt.Errorf("%s didn't say which records %s needs (its dns script printed none, or it isn't running)", app, domain)
	}
	tok, zoneID, zone, err := cfZone(domain)
	if err != nil {
		return DNSPlan{}, err
	}
	plan := DNSPlan{Domain: domain, App: app, Zone: zone, zoneID: zoneID, token: tok}
	for _, r := range recs {
		want, err := cfRecord(r.Type, r.Name, r.Value)
		if err != nil {
			return plan, err
		}
		b, err := cfDo(tok, "GET", "/zones/"+zoneID+"/dns_records?per_page=100&type="+r.Type+"&name="+url.QueryEscape(r.Name), nil)
		if err != nil {
			return plan, err
		}
		ch := DNSChange{Type: r.Type, Name: r.Name, Value: r.Value, Action: "add"}
		existing, _ := b["result"].([]any)
		for _, e := range existing {
			have, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if sameRecord(want, have) {
				ch.Action, ch.Old, ch.oldIDs = "ok", nil, nil
				break
			}
			if r.Type != "TXT" || (txtKind(r.Value) != "" && txtKind(r.Value) == txtKind(fmt.Sprint(have["content"]))) {
				ch.Action = "replace"
				ch.Old = append(ch.Old, cfShown(have))
				ch.oldIDs = append(ch.oldIDs, fmt.Sprint(have["id"]))
			}
		}
		plan.Changes = append(plan.Changes, ch)
	}
	return plan, nil
}

// SetMailDNS makes the plan's changes: deletes what each replaces, then adds the new record.
func (h *Host) SetMailDNS(domain string) error {
	plan, err := PlanMailDNS(domain)
	if err != nil {
		return err
	}
	n := 0
	deleted := map[string]bool{}
	for _, ch := range plan.Changes {
		if ch.Action == "ok" {
			continue
		}
		for _, id := range ch.oldIDs {
			if deleted[id] {
				continue
			}
			deleted[id] = true
			if _, err := cfDo(plan.token, "DELETE", "/zones/"+plan.zoneID+"/dns_records/"+id, nil); err != nil {
				return fmt.Errorf("removing the old %s %s: %v", ch.Type, ch.Name, err)
			}
		}
		rec, _ := cfRecord(ch.Type, ch.Name, ch.Value)
		if _, err := cfDo(plan.token, "POST", "/zones/"+plan.zoneID+"/dns_records", rec); err != nil {
			return fmt.Errorf("adding %s %s: %v", ch.Type, ch.Name, err)
		}
		h.say("%s %s %s %s", map[string]string{"add": "added", "replace": "replaced"}[ch.Action], ch.Type, ch.Name, ch.Value)
		n++
	}
	if n == 0 {
		h.say("%s's records were already right", domain)
	}
	return nil
}
