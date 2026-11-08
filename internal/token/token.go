// Package token is the NOOK_TOKEN secret: everything a deploy needs, as "nook1:" + base64url(JSON).
package token

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var (
	userRe = regexp.MustCompile(`^(nook|app)-[a-z0-9][a-z0-9-]*$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.:\[\]-]+$`)
)

const (
	prefix    = "nook1:"
	oldPrefix = "apphost1:" // tokens printed before the rename still work
)

type Token struct {
	V          int    `json:"v"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Dir        string `json:"dir"`
	Domain     string `json:"domain"`
	KnownHosts string `json:"knownHosts"`
	Key        string `json:"key"`
}

func (t Token) Encode() string {
	b, _ := json.Marshal(t)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func Decode(s string) (*Token, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("the NOOK_TOKEN secret is empty: add it to this repo (nook-admin create prints it)")
	}
	body, ok := strings.CutPrefix(s, prefix)
	if !ok {
		if body, ok = strings.CutPrefix(s, oldPrefix); !ok {
			return nil, errors.New("that doesn't look like a NOOK_TOKEN secret (it should start with nook1:)")
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(body, "="))
	if err != nil {
		return nil, errors.New("the NOOK_TOKEN secret is damaged; copy it again from nook-admin create")
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, errors.New("the NOOK_TOKEN secret is damaged; copy it again from nook-admin create")
	}
	for name, v := range map[string]string{"host": t.Host, "user": t.User, "domain": t.Domain, "knownHosts": t.KnownHosts, "key": t.Key} {
		if v == "" {
			return nil, errors.New("the NOOK_TOKEN secret is missing " + name)
		}
	}
	if t.Port == 0 {
		t.Port = 22
	}
	// these end up on ssh's command line: keep them from being read as options
	if !userRe.MatchString(t.User) || !hostRe.MatchString(t.Host) || t.Port < 1 || t.Port > 65535 {
		return nil, errors.New("the NOOK_TOKEN secret has a bad host, user or port; copy it again from nook-admin create")
	}
	return &t, nil
}
