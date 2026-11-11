package main

// API mode: a "nook_..." token deploys over the dashboard's HTTPS deploy API (POST /api/v1/sites/SITE)
// instead of SSH. Needs the `url` input (the dashboard's address).

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/token"
)

func apiMain(tok string) {
	base := strings.TrimRight(in("URL"), "/")
	if base == "" { // tokens from nook 1.6 on carry the dashboard's address
		if addr, _ := token.APIServer(tok); addr != "" {
			base = "https://" + addr
		}
	}
	if base == "" {
		fail("this token doesn't say which server it's for (it's from before nook 1.6): set the action's `url` input to the dashboard's address, like https://admin.example.com, or make a new token")
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, "/api/v1/sites"), "/api/v1")
	if !strings.HasPrefix(base, "https://") {
		fail("url must start with https://")
	}
	site := strings.ToLower(in("SITE"))
	if !names.Site(site) {
		fail("site %q must be a subdomain like \"blog\" or a hostname like \"client.com\"", site)
	}
	client := &http.Client{Timeout: 20 * time.Minute}
	call := func(method, path string, body io.Reader, ctype string) (int, []byte) {
		req, err := http.NewRequest(method, base+path, body)
		if err != nil {
			fail("%v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("User-Agent", "nook-deploy-action")
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := client.Do(req)
		if err != nil {
			fail("can't reach %s: %v", base, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, b
	}
	errOf := func(b []byte) string {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return e.Error
		}
		return strings.TrimSpace(string(b))
	}

	code, b := call("GET", "/api/v1/whoami", nil, "")
	if code != 200 {
		fail("token refused (%d): %s", code, errOf(b))
	}
	var who struct {
		App    string `json:"app"`
		Domain string `json:"domain"`
	}
	json.Unmarshal(b, &who)

	pr, closing := previewInfo()
	name, q := site, ""
	if pr > 0 {
		name, q = names.Preview(site, pr), "?pr="+strconv.Itoa(pr)
	}
	host := names.HostOf(name, who.Domain)
	url := "https://" + host

	if closing {
		code, b := call("DELETE", "/api/v1/sites/"+site+q, nil, "")
		if code != 200 {
			fail("couldn't remove %s (%d): %s", host, code, errOf(b))
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
	body, err := os.CreateTemp("", "nook-upload-")
	if err != nil {
		fail("%v", err)
	}
	defer os.Remove(body.Name())
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("site", "site.tgz")
	files, err := writeTarGz(part, dir)
	if err != nil {
		fail("packing %s: %v", dir, err)
	}
	if env := in("ENV"); env != "" {
		w, _ := mw.CreateFormFile("env", "deploy.env")
		io.WriteString(w, env+"\n")
	}
	mw.Close()
	size, _ := body.Seek(0, io.SeekCurrent)
	body.Seek(0, io.SeekStart)
	fmt.Printf("$ POST %s/api/v1/sites/%s%s (%d files, %d KB)\n", base, site, q, files, size/1024)
	code, b = call("POST", "/api/v1/sites/"+site+q, body, mw.FormDataContentType())
	var res struct {
		Output string `json:"output"`
		URL    string `json:"url"`
	}
	json.Unmarshal(b, &res)
	switch {
	case code == 200:
		fmt.Println(res.Output)
	case code == 409:
		fmt.Println(res.Output)
		fail("%s didn't go live: this app isn't allowed that hostname, it's reserved, or another app has it. Grant it on the dashboard's Domains page.", host)
	default:
		fail("deploy failed (%d): %s", code, errOf(b))
	}
	if res.URL != "" {
		url = res.URL
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

// previewInfo reads the PR number (0 on a push, or when previews are off) and whether the PR just closed.
func previewInfo() (int, bool) {
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
	return pr, pr > 0 && ev.Action == "closed"
}

// writeTarGz packs dir's files and folders (symlinked files are stored as their contents; the server
// accepts nothing else). Returns the number of files.
func writeTarGz(w io.Writer, dir string) (int, error) {
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		fi, err := os.Stat(p) // follows symlinks
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if d.Type()&fs.ModeSymlink != 0 {
				fmt.Printf("::warning::skipping symlinked folder %s\n", rel)
				return nil
			}
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: fi.ModTime()})
		case fi.Mode().IsRegular():
			mode := int64(0o644)
			if fi.Mode()&0o100 != 0 {
				mode = 0o755
			}
			if err := tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: mode, Size: fi.Size(), ModTime: fi.ModTime()}); err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			n++
			return err
		default:
			fmt.Printf("::warning::skipping %s (not a file or folder)\n", rel)
			return nil
		}
	})
	if err != nil {
		return n, err
	}
	if err := tw.Close(); err != nil {
		return n, err
	}
	return n, zw.Close()
}
