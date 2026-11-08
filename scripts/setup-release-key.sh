#!/bin/sh
# One-time: make the release signing key. The private half goes straight into the repo's
# RELEASE_SIGNING_KEY secret (never written to disk); the public half is written for you to commit.
# Run from the repo root on your own machine, with gh logged in.
set -eu
[ -f go.mod ] || { echo "run from the app-host repo root"; exit 1; }
if [ -s internal/update/signing.pub ]; then
  echo "internal/update/signing.pub already has a key. Delete it first if you really want a new one"
  echo "(servers on older releases will then refuse updates signed with the new key)."
  exit 1
fi
keys=$(go run ./cmd/release-sign keygen)
printf '%s\n' "$keys" | sed -n 's/^PRIVATE=//p' | gh secret set RELEASE_SIGNING_KEY
printf '%s\n' "$keys" | sed -n 's/^PUBLIC=//p' > internal/update/signing.pub
echo "Done. Commit internal/update/signing.pub, then tag a release: git tag v1.0.0 && git push origin v1.0.0"
