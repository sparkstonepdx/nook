// deploy-action is the GitHub Action behind sparkstonepdx/nook/deploy. With a deploy token ("nook_...") it
// uses the dashboard's HTTPS deploy API (api.go). With an older SSH token ("nook1:...") it uploads over SSH
// (rsync into builds/<site>/) and publishes with `nook site deploy`. Either way it handles PR previews, the
// PR comment and the job summary. Tokens and keys are never printed.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/token"
)

func in(name string) string { return strings.TrimSpace(os.Getenv("INPUT_" + name)) }

func fail(format string, a ...any) {
	fmt.Printf("::error::"+format+"\n", a...)
	os.Exit(1)
}

func appendFile(env, line string) {
	if p := os.Getenv(env); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			f.WriteString(line + "\n")
			f.Close()
		}
	}
}

type event struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
}

func main() {
	if raw := in("TOKEN"); token.IsAPI(raw) {
		apiMain(raw)
		return
	}
	tok, err := token.Decode(in("TOKEN"))
	if err != nil {
		fail("%v", err)
	}
	site := strings.ToLower(in("SITE"))
	if !names.Site(site) {
		fail("site %q must be a subdomain like \"blog\" or a hostname like \"client.com\"", site)
	}

	var ev event
	if p := os.Getenv("GITHUB_EVENT_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &ev)
		}
	}
	pr := 0
	if strings.HasPrefix(os.Getenv("GITHUB_EVENT_NAME"), "pull_request") && in("PREVIEWS") != "false" {
		pr = ev.Number
		if ev.PullRequest != nil {
			pr = ev.PullRequest.Number
		}
	}
	closing := pr > 0 && ev.Action == "closed"
	name := site
	if pr > 0 {
		name = names.Preview(site, pr)
	}
	host := names.HostOf(name, tok.Domain)
	url := "https://" + host

	// --- ssh setup: host key pinned, key in a private temp dir ---
	tmp, err := os.MkdirTemp("", "nook-")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)
	keyFile, khFile := filepath.Join(tmp, "key"), filepath.Join(tmp, "known_hosts")
	key := tok.Key
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	os.WriteFile(keyFile, []byte(key), 0o600)
	known := tok.KnownHosts
	if tok.Port != 22 && !strings.HasPrefix(known, "[") {
		if i := strings.IndexByte(known, ' '); i > 0 {
			known = fmt.Sprintf("[%s]:%d%s", known[:i], tok.Port, known[i:])
		}
	}
	os.WriteFile(khFile, []byte(known+"\n"), 0o600)
	sshOpts := []string{"-i", keyFile, "-o", "UserKnownHostsFile=" + khFile, "-o", "StrictHostKeyChecking=yes",
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=20", "-p", strconv.Itoa(tok.Port)}
	target := tok.User + "@" + tok.Host

	run := func(label, cmd string, args ...string) int {
		fmt.Println("$ " + label)
		c := exec.Command(cmd, args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return 255
		}
		return 0
	}
	ssh := func(remote string) int {
		return run("ssh "+target+" "+remote, "ssh", append(append([]string{}, sshOpts...), target, remote)...)
	}

	if closing {
		if ssh("nook site rm "+name) != 0 {
			fail("couldn't remove %s", host)
		}
		comment(site, pr, fmt.Sprintf("Preview removed: ~~%s~~", url))
		appendFile("GITHUB_STEP_SUMMARY", fmt.Sprintf("Removed preview **%s**", host))
		return
	}

	dir := in("DIR")
	if dir == "" {
		dir = "dist"
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fail("dir %q doesn't exist. Did the build step run?", dir)
	}
	src := strings.TrimRight(dir, "/") + "/"
	upload := "builds/" + name + "/"
	rsh := "ssh " + strings.Join(sshOpts, " ") // temp paths have no spaces
	if run("rsync "+src+" "+target+":"+upload, "rsync", "-a", "--delete", "-e", rsh, "--", src, target+":"+upload) != 0 {
		fail("upload failed")
	}
	switch code := ssh("nook site deploy " + upload + " " + name); code {
	case 0:
	case 2:
		fail("%s didn't go live: this app isn't allowed that hostname, it's reserved, or another app has it. Grant it on the dashboard's Domains page.", host)
	default:
		fail("deploy failed (exit %d)", code)
	}
	waitHTTPS(url)
	appendFile("GITHUB_OUTPUT", "url="+url)
	appendFile("GITHUB_OUTPUT", "site="+name)
	note := ""
	if pr > 0 {
		note = fmt.Sprintf(" (preview for #%d)", pr)
		sha := os.Getenv("GITHUB_SHA")
		if len(sha) > 7 {
			sha = sha[:7]
		}
		comment(site, pr, fmt.Sprintf("Preview: %s\n\n<sub>Updated for %s</sub>", url, sha))
	}
	appendFile("GITHUB_STEP_SUMMARY", fmt.Sprintf("Deployed **[%s](%s)**%s", host, url, note))
	fmt.Println("Live at " + url)
}

// comment adds or updates one PR comment per site (found by a hidden marker).
func comment(site string, pr int, body string) {
	tokenStr, repo := in("GITHUB_TOKEN"), os.Getenv("GITHUB_REPOSITORY")
	if tokenStr == "" || repo == "" || pr == 0 {
		return
	}
	apiBase := os.Getenv("GITHUB_API_URL")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	marker := "<!-- nook:" + site + " -->"
	client := &http.Client{Timeout: 20 * time.Second}
	call := func(method, path string, payload any) (*http.Response, error) {
		var b []byte
		if payload != nil {
			b, _ = json.Marshal(payload)
		}
		req, _ := http.NewRequest(method, apiBase+"/repos/"+repo+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "nook-deploy")
		return client.Do(req)
	}
	warn := func(msg string) {
		fmt.Printf("::warning::couldn't comment on the PR (%s). Give the workflow \"pull-requests: write\" permission.\n", msg)
	}
	resp, err := call("GET", fmt.Sprintf("/issues/%d/comments?per_page=100", pr), nil)
	if err != nil {
		warn(err.Error())
		return
	}
	var list []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Type string `json:"type"`
		} `json:"user"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	full := map[string]string{"body": marker + "\n" + body}
	for _, c := range list {
		if c.User.Type == "Bot" && strings.HasPrefix(c.Body, marker) { // only our own comment, not one quoting the marker
			resp, err = call("PATCH", fmt.Sprintf("/issues/comments/%d", c.ID), full)
			if err == nil && resp.StatusCode >= 300 {
				err = fmt.Errorf("%s", resp.Status)
			}
			if err != nil {
				warn(err.Error())
			}
			return
		}
	}
	resp, err = call("POST", fmt.Sprintf("/issues/%d/comments", pr), full)
	if err == nil && resp.StatusCode >= 300 {
		err = fmt.Errorf("%s", resp.Status)
	}
	if err != nil {
		warn(err.Error())
	}
}

// waitHTTPS waits for the site to answer over HTTPS (a new hostname needs its certificate first).
// Not answering isn't a failure: DNS may not point at the server yet.
func waitHTTPS(url string) {
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 12; i++ {
		if resp, err := client.Get(url); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(5 * time.Second)
	}
	fmt.Printf("::warning::%s doesn't answer over HTTPS yet. Check that its DNS points at the server; the certificate is issued on first contact.\n", url)
}
