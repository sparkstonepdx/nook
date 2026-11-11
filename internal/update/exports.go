package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Repo is the GitHub repo releases (and the store catalog) come from.
func Repo() string { r, _, _ := settings(); return r }

// Download fetches url (https only, 200 MB cap). The GitHub token is sent only to api.github.com.
func Download(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, apiBase()) {
		return nil, fmt.Errorf("refusing a non-https download: %s", url)
	}
	_, token, _ := settings()
	if !strings.HasPrefix(url, "https://api.github.com/") {
		token = ""
	}
	return get(url, token, "application/octet-stream")
}

// ReleaseDownloadBase is where the repo's release assets are served (overridable for tests with UPDATE_API).
func ReleaseDownloadBase() string {
	if v := apiBase(); v != "https://api.github.com" {
		return v + "/download"
	}
	return "https://github.com/" + Repo() + "/releases/download"
}

// VerifySigned checks a detached base64 ed25519 signature over data with the built-in key.
func VerifySigned(data, sig []byte) error { return VerifyChecksums(data, sig) }

// Notify posts title/msg to UPDATE_NOTIFY_URL (ntfy, Discord, Slack or any plain-body webhook), if set.
func Notify(title, msg string) error {
	_, _, url := settings()
	if url == "" {
		return nil
	}
	var body []byte
	ctype := "text/plain"
	switch {
	case strings.Contains(url, "discord.com/api/webhooks"):
		body, _ = json.Marshal(map[string]string{"content": title + ": " + msg})
		ctype = "application/json"
	case strings.Contains(url, "hooks.slack.com"):
		body, _ = json.Marshal(map[string]string{"text": title + ": " + msg})
		ctype = "application/json"
	default:
		body = []byte(msg)
	}
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Title", title)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// TestServer reports whether UPDATE_API points releases at a test server, and its download base.
func TestServer() (string, bool) {
	if b := apiBase(); b != "https://api.github.com" {
		return b + "/download", true
	}
	return "", false
}
