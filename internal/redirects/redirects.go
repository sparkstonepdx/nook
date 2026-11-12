// Package redirects reads a static site's _redirects file, the format Netlify and Cloudflare Pages use:
//
//	# comment
//	/old        /new           301
//	/blog/:slug /posts/:slug   302
//	/game/*     /app.html      200
//	/docs/*     https://docs.example.com/:splat
//	/forced     /elsewhere     301!
//
// One rule a line: from, to, then an optional status (301 when missing). 200 serves `to` at `from` (a rewrite);
// 301, 302, 303, 307 and 308 redirect. `:name` matches one path segment and `*` (at the end) matches the rest as
// `:splat`; both can be used in `to`. As on Netlify, a rule only applies when no file exists at the path, unless
// its status ends in `!`. The first matching rule wins. Lines this can't serve (conditions, query matches,
// proxying to another site, other statuses) are skipped with a reason.
//
// Everything accepted is restricted to characters that can't break out of a Caddyfile token.
package redirects

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxRules is the most rules one site may have; the rest are skipped.
const MaxRules = 500

// MaxBytes is the largest _redirects file read.
const MaxBytes = 64 << 10

// Rule is one line, ready for Caddy: Regexp matches the path (named groups for :name and splat), and Target
// gives `to` with Caddy placeholders for them.
type Rule struct {
	From, To string
	Status   int
	Force    bool
	Regexp   string
	target   string // to, with {re.@.name} where a capture goes
}

// Target is `to` for a path_regexp matcher of that name: :slug becomes {re.MATCHER.slug}.
func (r Rule) Target(matcher string) string {
	return strings.ReplaceAll(r.target, "{re.@.", "{re."+matcher+".")
}

// Redirect says whether the rule redirects rather than rewrites.
func (r Rule) Redirect() bool { return r.Status != 200 }

var (
	pathChars = `A-Za-z0-9._~%!$&'()+,;=:@/\-`
	fromRE    = regexp.MustCompile(`^/[` + pathChars + `*]*$`)
	toPathRE  = regexp.MustCompile(`^/[` + pathChars + `?]*$`)
	toURLRE   = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[` + pathChars + `?]*)?$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
	statuses  = map[int]bool{200: true, 301: true, 302: true, 303: true, 307: true, 308: true}
)

// Parse reads a _redirects file. It returns the rules it can serve and a reason for each line it skipped.
func Parse(text string) ([]Rule, []string) {
	var rules []Rule
	var skipped []string
	for i, line := range strings.Split(text, "\n") {
		if c := strings.Index(line, "#"); c >= 0 {
			line = line[:c]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		skip := func(why string) {
			skipped = append(skipped, fmt.Sprintf("line %d (%s): %s", i+1, strings.Join(f, " "), why))
		}
		if len(rules) == MaxRules {
			skip(fmt.Sprintf("more than %d rules", MaxRules))
			continue
		}
		r, why := parseLine(f)
		if why != "" {
			skip(why)
			continue
		}
		rules = append(rules, r)
	}
	return rules, skipped
}

func parseLine(f []string) (Rule, string) {
	if len(f) < 2 {
		return Rule{}, "needs a from and a to"
	}
	if len(f) > 3 || strings.Contains(f[1], "=") && !strings.HasPrefix(f[1], "/") && !strings.Contains(f[1], "://") {
		return Rule{}, "conditions and query matches aren't supported"
	}
	r := Rule{From: f[0], To: f[1], Status: 301}
	if len(f) == 3 {
		s := f[2]
		r.Force = strings.HasSuffix(s, "!")
		n, err := strconv.Atoi(strings.TrimSuffix(s, "!"))
		if err != nil || !statuses[n] {
			return Rule{}, "status must be 200, 301, 302, 303, 307 or 308"
		}
		r.Status = n
	}
	if !fromRE.MatchString(r.From) {
		return Rule{}, "from must be a path like /old or /blog/:slug or /game/*"
	}
	external := strings.Contains(r.To, "://")
	if external && !toURLRE.MatchString(r.To) || !external && !toPathRE.MatchString(r.To) {
		return Rule{}, "to must be a path like /new or an http(s) URL, without spaces, quotes, braces or #"
	}
	if external && r.Status == 200 {
		return Rule{}, "200 to another site would proxy it; only redirects can go to a URL"
	}

	// from becomes an anchored regexp; :name and a trailing * become named groups
	var re strings.Builder
	names := map[string]bool{}
	segs := strings.Split(r.From[1:], "/")
	for i, seg := range segs {
		re.WriteString("/")
		switch {
		case seg == "*" && i == len(segs)-1:
			re.WriteString("(?P<splat>.*)")
			names["splat"] = true
		case strings.HasSuffix(seg, "*") && i == len(segs)-1 && !strings.Contains(seg[:len(seg)-1], "*"):
			re.WriteString(regexp.QuoteMeta(seg[:len(seg)-1]) + "(?P<splat>.*)")
			names["splat"] = true
		case strings.Contains(seg, "*"):
			return Rule{}, "* only works at the end of from"
		case strings.HasPrefix(seg, ":"):
			n := seg[1:]
			if !nameRE.MatchString(n) || n == "splat" || names[n] {
				return Rule{}, fmt.Sprintf("bad placeholder %q", seg)
			}
			names[n] = true
			re.WriteString("(?P<" + n + ">[^/]+)")
		default:
			re.WriteString(regexp.QuoteMeta(seg))
		}
	}
	if !strings.HasSuffix(r.From, "/") && !names["splat"] {
		re.WriteString("/?") // /old and /old/ both match, as on Netlify
	}
	r.Regexp = "^" + re.String() + "$"
	if _, err := regexp.Compile(r.Regexp); err != nil {
		return Rule{}, "from doesn't make a valid pattern"
	}

	// :name in to becomes the matcher's capture; anything else with a colon (https:, ports) is left alone
	r.target = regexp.MustCompile(`:([A-Za-z][A-Za-z0-9_]*)`).ReplaceAllStringFunc(r.To, func(m string) string {
		if names[m[1:]] {
			return "{re.@." + m[1:] + "}"
		}
		return m
	})
	return r, ""
}
