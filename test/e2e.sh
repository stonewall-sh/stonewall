#!/bin/sh
# End-to-end check of the sandbox on the current OS. Usage: test/e2e.sh [stonewall-binary]
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
BIN=${1:-$ROOT/stonewall}
[ -x "$BIN" ] || (cd "$ROOT" && go build -o "$BIN" .)
BIN=$(cd "$(dirname "$BIN")" && pwd -P)/$(basename "$BIN")

TMP=$(mktemp -d) && TMP=$(cd "$TMP" && pwd -P)
trap 'rm -rf "$TMP"' EXIT
PROJ=$TMP/proj
HOME=$TMP/home
export HOME
mkdir -p "$PROJ/.git" "$PROJ/secrets" "$PROJ/src" "$PROJ/policies" "$HOME/.ssh" "$HOME/exposed" "$HOME/exposed-ro" "$TMP/ro-outside"
echo 'ref: refs/heads/main' > "$PROJ/.git/HEAD"
echo 'TOKEN=secret' > "$PROJ/.env"
echo 'key' > "$PROJ/secrets/key"
echo 'id' > "$HOME/.ssh/id"
echo 'ok' > "$HOME/exposed/ok"
echo 'ok' > "$HOME/exposed-ro/ok"
echo 'ok' > "$TMP/ro-outside/ok"
# The local include grants git and curl; the policy file is applied last and takes curl away again.
printf 'bin:\n  allowed: [git, curl]\n' > "$PROJ/policies/extra.yml"
# A remote include already reviewed and cached: resolved from .stonewall/cache/remote-policies without any network.
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
mkdir -p "$PROJ/.stonewall/cache/remote-policies"
printf 'bin:\n  allowed: [cat]\n' > "$TMP/cached.yml"
SHA=$(sha256 "$TMP/cached.yml")
CACHED=.stonewall/cache/remote-policies/base-$SHA.yml
cp "$TMP/cached.yml" "$PROJ/$CACHED"
printf 'policies:\n  "https://policies.invalid/base.yml":\n    sha256: %s\n    fetched: %s\n' "$SHA" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$PROJ/.stonewall/lock.yml"
printf 'include: [policies/extra.yml, "https://policies.invalid/base.yml"]\nbin:\n  allowed: [sh, grep]\n  denied: [curl]\nproject:\n  readonly: [.git]\n  hidden: [.env, secrets/]\nexpose:\n  write: [~/exposed]\n  read: [~/exposed-ro, %s]\n' "$TMP/ro-outside" > "$PROJ/.stonewall.yml"

fail=0
check() { # check <name> <want exit 0|1> <shell command, run inside the sandbox from $PROJ>
	if (cd "$PROJ" && "$BIN" sh -c "$3") >/dev/null 2>&1; then got=0; else got=1; fi
	if [ "$got" = "$2" ]; then echo "ok    $1"; else echo "FAIL  $1 (exit $got, want $2)"; fail=1; fi
}
check "project is writable"          0 'echo hi > src/new && test -f src/new'
check ".git is readonly"             1 'echo x > .git/x'
check ".git stays readable"          0 'grep -q refs .git/HEAD'
check ".stonewall.yml is readonly"   1 'echo x >> .stonewall.yml'
check "included policy file is readonly" 1 'echo x >> policies/extra.yml'
check "cached remote policy is readonly" 1 "echo x >> $CACHED"
check "policy lock is readonly"          1 'echo x >> .stonewall/lock.yml'
check ".env content is hidden"       1 'grep -q TOKEN .env'
check "secrets/ content is hidden"   1 'grep -q key secrets/key'
check "~/.ssh is inaccessible"       1 'grep -q id "$HOME/.ssh/id"'
check "exposed home path is visible" 0 'grep -q ok "$HOME/exposed/ok"'
check "read-only exposed path is readable"    0 'grep -q ok "$HOME/exposed-ro/ok"'
check "read-only exposed path is not writable" 1 'echo x >> "$HOME/exposed-ro/ok"'
check "read-only exposed path outside HOME is readable"     0 "grep -q ok $TMP/ro-outside/ok"
check "read-only exposed path outside HOME is not writable" 1 "echo x >> $TMP/ro-outside/ok"
check "git is on PATH"               0 'command -v git'
check "cached remote policy is applied" 0 'command -v cat'
check "curl is not on PATH"          1 'command -v curl'
check "PATH is only the bin dir"     0 'case "$PATH" in */stonewall-bin-*) [ "${PATH#*:}" = "$PATH" ];; *) false;; esac'

if (cd "$PROJ" && "$BIN" --dry-run sh -c 'touch src/dry') | grep -q 'stonewall-bin-' && [ ! -e "$PROJ/src/dry" ]; then
	echo "ok    --dry-run prints without launching"
else
	echo "FAIL  --dry-run"; fail=1
fi

# The first run writes the scaffold without asking; the launch then still fails, because the scaffold's remote
# includes are not trusted yet. Fail closed, so ignore the exit.
mkdir -p "$TMP/fresh/.git" "$TMP/fresh/sub"
(cd "$TMP/fresh/sub" && "$BIN" --plain sh -c true) >/dev/null 2>&1 </dev/null || true
if grep -q '^include:' "$TMP/fresh/.stonewall.yml" 2>/dev/null && grep -q 'stonewall.sh/policies/base.yml' "$TMP/fresh/.stonewall.yml"; then
	echo "ok    first run scaffolds .stonewall.yml at the project root"
else
	echo "FAIL  scaffold"; fail=1
fi

got=0; (cd "$PROJ" && "$BIN" sh -c 'exit 7') >/dev/null 2>&1 || got=$?
if [ "$got" = 7 ]; then echo "ok    agent exit code propagates"; else echo "FAIL  exit code ($got, want 7)"; fail=1; fi

if [ "$fail" = 0 ]; then echo "e2e: all passed on $(uname -s)"; else echo "e2e: FAILURES on $(uname -s)"; exit 1; fi
