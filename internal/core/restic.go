package core

// restic, pinned: nook downloads the release it was built for from GitHub, checks it against the SHA-256 digest
// GitHub publishes for the asset, and keeps it at /usr/local/lib/nook/restic. A newer nook can move the pin.

import (
	"compress/bzip2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sparkstonepdx/nook/internal/update"
)

var (
	resticVersion = "0.19.1"
	resticRelease = "https://api.github.com/repos/restic/restic/releases/tags/v"
	// the downloads, as vars for tests: https only, through the updater's client
	resticGet   = update.Download
	resticGetTo = update.DownloadTo
)

// EnsureRestic installs the pinned restic if it isn't there yet.
func EnsureRestic(out io.Writer) error {
	if b, err := exec.Command(resticBin, "version").Output(); err == nil && strings.Contains(string(b), "restic "+resticVersion) {
		return nil
	}
	fmt.Fprintf(out, "installing restic %s\n", resticVersion)
	meta, err := resticGet(resticRelease + resticVersion)
	if err != nil {
		return fmt.Errorf("restic release: %v", err)
	}
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(meta, &rel); err != nil {
		return fmt.Errorf("restic release: %v", err)
	}
	want := fmt.Sprintf("restic_%s_linux_%s.bz2", resticVersion, runtime.GOARCH)
	var url, sum string
	for _, a := range rel.Assets {
		if a.Name == want {
			url, sum = a.URL, strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if url == "" || len(sum) != 64 {
		return fmt.Errorf("restic %s has no %s with a checksum", resticVersion, want)
	}
	os.MkdirAll(filepath.Dir(resticBin), 0o755)
	dl, err := os.CreateTemp(filepath.Dir(resticBin), ".restic-dl-")
	if err != nil {
		return err
	}
	defer os.Remove(dl.Name())
	defer dl.Close()
	h := sha256.New()
	if err := resticGetTo(url, io.MultiWriter(dl, h), 100<<20, nil); err != nil {
		return fmt.Errorf("restic download: %v", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		return errors.New("the restic download doesn't match GitHub's checksum: refusing it")
	}
	if _, err := dl.Seek(0, io.SeekStart); err != nil {
		return err
	}
	bin, err := os.CreateTemp(filepath.Dir(resticBin), ".restic-")
	if err != nil {
		return err
	}
	defer os.Remove(bin.Name())
	if _, err := io.Copy(bin, io.LimitReader(bzip2.NewReader(dl), 200<<20)); err != nil {
		bin.Close()
		return fmt.Errorf("restic unpack: %v", err)
	}
	bin.Close()
	os.Chmod(bin.Name(), 0o755)
	return os.Rename(bin.Name(), resticBin)
}

func goarch() string { return runtime.GOARCH }
