package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

func repoPath(app string) string { return filepath.Join(config.StateDir, app+".repo") }

func setAppRepo(app, repo string) { config.WriteAtomic(repoPath(app), []byte(repo+"\n"), 0o600) }

// SetAppRepo records the GitHub repo that deploys app (used in its instructions).
func SetAppRepo(app, repo string) error {
	if !repoRE.MatchString(repo) {
		return fmt.Errorf("repo must look like owner/name")
	}
	setAppRepo(app, repo)
	return nil
}

// AppRepo is the GitHub repo given when the app was created ("" if none).
func AppRepo(app string) string {
	b, _ := os.ReadFile(repoPath(app))
	return strings.TrimSpace(string(b))
}

// Instructions is everything a repo needs to deploy to app: the same text `nook-admin create` prints, any
// time later. token is shown only when it was just made; otherwise the text says where to make one. site
// defaults to the app's name.
func Instructions(app, token, site string) string { return instructions(app, token, site, true) }

// InstructionsNoToken is the instructions without the token section, for the dashboard, which shows the
// token (and how to store it) on its own.
func InstructionsNoToken(app, site string) string { return instructions(app, "", site, false) }

func instructions(app, token, site string, tokenSection bool) string {
	s := config.Load()
	if site == "" || !names.Site(site) {
		site = app
	}
	repo := AppRepo(app)
	if repo == "" {
		repo = "OWNER/REPO"
	}
	base := DeployBase(s)
	host := names.HostOf(site, s.BaseDomain)
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("Deploying to app %s (site %s → https://%s)\n", app, site, host)
	if strings.Contains(strings.TrimPrefix(base, "https://"), ":") {
		w("\nNOTE: the dashboard has no address yet, so these URLs use the server's IP and a self-signed\ncertificate, which curl and GitHub refuse. Set the address on the dashboard's Domains page first.\n")
	}

	if tokenSection {
		w("\n================ 1. Deploy token (GitHub secret NOOK_TOKEN) ================\n\n")
		if token != "" {
			w("  gh secret set NOOK_TOKEN -R %s      # paste this when it asks:\n\n  %s\n\n", repo, token)
			w("Shown once; the server keeps only its hash.\n")
		} else {
			w("Tokens are shown only when made. Make one and store it:\n\n")
			w("  dashboard → Apps → %s → Deploy → New token     (or: nook-admin token create %s --label %s)\n", app, app, repo)
			w("  gh secret set NOOK_TOKEN -R %s\n", repo)
		}
		w("\nRevoke tokens on the same page. Any CI works: a deploy is one HTTPS request.\n")
	}

	w("\n================ 2. Workflow: .github/workflows/deploy.yml ================\n\n")
	w(`name: deploy
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]
concurrency:
  group: deploy-${{ github.event.pull_request.number || github.ref }}
env:
  NOOK: %s%s
  NOOK_TOKEN: ${{ secrets.NOOK_TOKEN }}
  PR: ${{ github.event.pull_request.number }}
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - if: github.event.action == 'closed'
        run: |
          curl -fsS -X DELETE -H "Authorization: Bearer $NOOK_TOKEN" "$NOOK?pr=$PR"
      - if: github.event.action != 'closed'
        uses: actions/checkout@v4
      - if: github.event.action != 'closed'
        run: npm ci && npm run build      # your build; skip for plain files
      - if: github.event.action != 'closed'
        run: |
          tar -C dist -czf site.tgz .     # the folder to publish
          curl --fail-with-body -sS -H "Authorization: Bearer $NOOK_TOKEN" -F site=@site.tgz "$NOOK${PR:+?pr=$PR}"
`, base, site)
	w(`
Pushes to main publish %[1]s; a pull request publishes a preview (%[2]s) and the
closed PR removes it. Deploy more sites from the same repo by repeating the step with another site name.

Prefer a PR comment with the preview link? Use the action instead of the curl steps:

      - uses: sparkstonepdx/nook/deploy@v1
        with:
          token: ${{ secrets.NOOK_TOKEN }}   # carries this server's address
          site: %[1]s
          dir: dist
    (with permissions: pull-requests: write)

The API, for other CI or by hand:
  POST   %[4]sSITE[?pr=N]   body: the folder as .tar.gz (raw, or multipart field "site")
  DELETE %[4]sSITE[?pr=N]   remove it
  GET    %[3]s/api/v1/whoami
`, site, names.Preview(site, 12), strings.TrimSuffix(base, "/api/v1/sites/"), base)

	w(`
================ 3. What to publish ================

  a folder of files                   static site (200.html = SPA fallback, 404.html = not found)
  + an executable "run"               web worker: an HTTP server on $PORT at https://%[1]s
  + nook.json {"kind": "service"}     runs on $PORT with no hostname (a database, a queue, ...)
  + nook.json {"kind": "cron",        runs on a schedule, no port
      "schedule": "*/15 * * * *"}
  + nook.json {"needs": ["postgres"]} asks for a shared service (see 5)
  + nook.json {"health": "/healthz"}  a path that answers under 400 when it works: checked after each
                                      deploy (a failing one fails the job) and on the dashboard
  nook.json {"cors": true}           static site: any origin may fetch its files

Site names: "%[2]s" is %[1]s; a hostname like client.com needs a grant (dashboard → Domains).
Keep data in $HOME/data (it survives deploys); running sites get $PORT, $SITE and $SITE_HOST.
`, host, site)

	envURL := strings.TrimSuffix(base, "sites/") + "env"
	w(`
================ 4. Environment and secrets ================

Set on the server: dashboard → Apps → %[1]s → Environment (whole app, one site, or a site's previews),
or nook-admin env set %[1]s KEY [--site SITE | --previews SITE]. Previews never get a site's own values.

From CI, push secrets without deploying (write-only: nothing can read them back). This sends every repo
secret named PROD_* as the app's env, minus the prefix (PROD_DATABASE_URL becomes DATABASE_URL):

      - if: github.event.action != 'closed'
        env:
          SECRETS: ${{ toJSON(secrets) }}
        run: |
          printf '%%s' "$SECRETS" | curl --fail-with-body -sS -H "Authorization: Bearer $NOOK_TOKEN" \
            -H 'Content-Type: application/json' --data-binary @- "%[2]s?prefix=PROD_"

  %[2]s                        the app's "sent by CI" values (every site; your dashboard values still win)
  %[2]s?site=SITE              one site's values ("sent by deploys")
  body: .env, JSON {"KEY": "value"}, or -F env.KEY=value      ?merge=1 adds instead of replacing
  ?unset=KEY1,KEY2 removes      ?prefix=P_ keeps only P_* names, without the P_
GITHUB_TOKEN and NOOK_TOKEN are never stored. Running sites restart with the new values.

Or send a site's values with its deploy:  curl ... -F site=@site.tgz -F "env.API_KEY=$API_KEY" "$NOOK"
`, app, envURL)

	w(`
================ 5. Shared services ================

Add "needs": ["postgres"] (or email, alerts, analytics, s3, ...) to a site's nook.json and deploy; then
grant it on the dashboard's Services page (or nook-admin bind %s postgres). The app gets standard env vars,
e.g. DATABASE_URL for its own private database.
`, app)
	if !tokenSection { // the sections after the token's move up one
		return sectionNumRE.ReplaceAllStringFunc(b.String(), func(m string) string {
			d := sectionNumRE.FindStringSubmatch(m)[1]
			return "================ " + string(rune(d[0]-1)) + ". "
		})
	}
	return b.String()
}

var sectionNumRE = regexp.MustCompile(`================ ([2-9])\. `)
