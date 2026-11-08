// Package token is the APP_HOST secret: everything a deploy needs, as "apphost1:" + base64url(JSON).
package token

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

const prefix = "apphost1:"

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
		return nil, errors.New("the APP_HOST secret is empty: add it to this repo (app-host create prints it)")
	}
	if !strings.HasPrefix(s, prefix) {
		return nil, errors.New("that doesn't look like an APP_HOST secret (it should start with apphost1:)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s[len(prefix):], "="))
	if err != nil {
		return nil, errors.New("the APP_HOST secret is damaged; copy it again from app-host create")
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, errors.New("the APP_HOST secret is damaged; copy it again from app-host create")
	}
	for name, v := range map[string]string{"host": t.Host, "user": t.User, "domain": t.Domain, "knownHosts": t.KnownHosts, "key": t.Key} {
		if v == "" {
			return nil, errors.New("the APP_HOST secret is missing " + name)
		}
	}
	if t.Port == 0 {
		t.Port = 22
	}
	return &t, nil
}
