// Package envfile reads and writes systemd EnvironmentFile content.
package envfile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sparkstonepdx/nook/internal/names"
)

// Vars keeps insertion order so files stay stable across edits.
type Vars struct {
	keys []string
	m    map[string]string
}

func New() *Vars { return &Vars{m: map[string]string{}} }

func (v *Vars) Set(k, val string) {
	if _, ok := v.m[k]; !ok {
		v.keys = append(v.keys, k)
	}
	v.m[k] = val
}

func (v *Vars) Unset(k string) {
	if _, ok := v.m[k]; !ok {
		return
	}
	delete(v.m, k)
	for i, x := range v.keys {
		if x == k {
			v.keys = append(v.keys[:i], v.keys[i+1:]...)
			break
		}
	}
}

func (v *Vars) Get(k string) (string, bool) { s, ok := v.m[k]; return s, ok }
func (v *Vars) Keys() []string              { return append([]string(nil), v.keys...) }
func (v *Vars) Len() int                    { return len(v.keys) }

// Parse accepts .env style lines: KEY=value, KEY="value", KEY='value', optional "export ". Others are skipped.
func Parse(text string) *Vars {
	v := New()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		i := strings.IndexByte(line, '=')
		if i <= 0 || strings.HasPrefix(line, "#") {
			continue
		}
		k, val := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		if !names.EnvKey(k) {
			continue
		}
		switch {
		case len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"':
			val = unescape(val[1 : len(val)-1])
		case len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'':
			val = val[1 : len(val)-1]
		}
		v.Set(k, val)
	}
	return v
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Check validates one assignment the way app env and the dashboard require.
func Check(k, val string) error {
	if !names.EnvKey(k) {
		return fmt.Errorf("%q isn't a valid name (letters, digits, _; not starting with a digit)", k)
	}
	if names.ReservedEnv[k] {
		return fmt.Errorf("%s is set by nook itself", k)
	}
	if strings.ContainsAny(val, "\r\n\x00") {
		return fmt.Errorf("%s: values can't contain newlines", k)
	}
	return nil
}

// Format writes systemd-safe KEY="value" lines (\ and " escaped), with a header comment.
func Format(v *Vars, header string) string {
	var b strings.Builder
	if header != "" {
		b.WriteString("# " + header + "\n")
	}
	for _, k := range v.keys {
		// escape what systemd (and a shell sourcing the file) treat specially inside double quotes, so
		// values reach the app exactly as written: no $VAR or `cmd` expansion
		val := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(v.m[k])
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, val)
	}
	return b.String()
}

// Filter keeps only valid, non-reserved keys with single-line values (for files apps write themselves).
func Filter(v *Vars) *Vars {
	out := New()
	for _, k := range v.keys {
		if Check(k, v.m[k]) == nil {
			out.Set(k, v.m[k])
		}
	}
	return out
}

// Sorted returns keys alphabetically (for display).
func (v *Vars) Sorted() []string { k := v.Keys(); sort.Strings(k); return k }
