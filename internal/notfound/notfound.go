// Package notfound is the server-wide 404 page: what a static site without its own 404.html shows for a path it
// doesn't have. Caddy reads it from Dir on each miss, so a change shows up without a reload. Edited on the
// dashboard's Domains page; an empty page puts the default back.
package notfound

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
)

//go:embed default.html
var Default []byte

// Dir holds 404.html; a variable so tests can point it elsewhere.
var Dir = "/etc/caddy/nook-404"

// Max is the largest page the dashboard accepts.
const Max = 256 << 10

func path() string { return filepath.Join(Dir, "404.html") }

// Read returns the page and whether it's been changed from the default. A missing file reads as the default.
func Read() (string, bool, error) {
	b, err := os.ReadFile(path())
	if errors.Is(err, os.ErrNotExist) {
		return string(Default), false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), !bytes.Equal(b, Default), nil
}

// Write replaces the page; an empty one restores the default.
func Write(html string) error {
	b := []byte(html)
	if strings.TrimSpace(html) == "" {
		b = Default
	}
	if len(b) > Max {
		return fmt.Errorf("the page is %d KB; the most is %d KB", len(b)>>10, Max>>10)
	}
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return err
	}
	return config.WriteAtomic(path(), b, 0o644)
}

// Ensure writes the default page if there's none yet (install and upgrade), keeping one that's been edited.
func Ensure() error {
	if _, err := os.Stat(path()); err == nil {
		return nil
	}
	return Write("")
}
