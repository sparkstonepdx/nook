package redirects

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	text := `# the apps' routes
/play       /app.html 200
/game/*     /app.html 200
/sync /sync.html 200   # clean URL
/blog/:slug /posts/:slug 302
/docs/*     https://docs.example.com/v2/:splat
/old        /new
/forced     /elsewhere 301!

/geo /us 302 Country=us
/q id=:id /items/:id 301
/bad {path} 301
/x /y 418
/p https://other.example.com/ 200
/a*/b /c
/spa/:slug /x/"q" 301
`
	rules, skipped := Parse(text)
	if len(rules) != 7 {
		t.Fatalf("rules: %d %+v\nskipped: %v", len(rules), rules, skipped)
	}
	if len(skipped) != 7 {
		t.Fatalf("skipped: %d %v", len(skipped), skipped)
	}
	cases := []struct {
		i            int
		path         string
		match        bool
		target       string
		status       int
		force, redir bool
	}{
		{0, "/play", true, "/app.html", 200, false, false},
		{0, "/play/", true, "/app.html", 200, false, false},
		{0, "/player", false, "", 200, false, false},
		{1, "/game/abc123", true, "/app.html", 200, false, false},
		{3, "/blog/hello-world", true, "/posts/{re.r3.slug}", 302, false, true},
		{3, "/blog/a/b", false, "", 302, false, true},
		{4, "/docs/a/b", true, "https://docs.example.com/v2/{re.r4.splat}", 301, false, true},
		{5, "/old", true, "/new", 301, false, true},
		{6, "/forced", true, "/elsewhere", 301, true, true},
	}
	for _, c := range cases {
		r := rules[c.i]
		if got := regexp.MustCompile(r.Regexp).MatchString(c.path); got != c.match {
			t.Errorf("%s ~ %s: %v", r.Regexp, c.path, got)
		}
		if !c.match {
			continue
		}
		if tgt := r.Target(fmt.Sprintf("r%d", c.i)); tgt != c.target || r.Status != c.status || r.Force != c.force || r.Redirect() != c.redir {
			t.Errorf("rule %d: target %q status %d force %v redirect %v", c.i, tgt, r.Status, r.Force, r.Redirect())
		}
	}
	for _, r := range rules {
		for _, bad := range []string{"\n", " ", "{path", "\"", "`", "\\"} {
			if tgt := r.Target("m"); strings.Contains(tgt, bad) {
				t.Errorf("target %q holds %q", tgt, bad)
			}
		}
		if strings.ContainsAny(r.Regexp, "`\n ") {
			t.Errorf("regexp %q could break the token", r.Regexp)
		}
	}
}

func TestMaxRules(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxRules+3; i++ {
		fmt.Fprintf(&b, "/r%d /x 301\n", i)
	}
	rules, skipped := Parse(b.String())
	if len(rules) != MaxRules || len(skipped) != 3 {
		t.Fatalf("%d rules, %d skipped", len(rules), len(skipped))
	}
}
