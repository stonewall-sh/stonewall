#!/bin/sh
# Validate every official policy against the schema. Usage: test/policies.sh [stonewall-binary]
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
BIN=${1:-$ROOT/stonewall}
[ -x "$BIN" ] || (cd "$ROOT" && go build -o "$BIN" .)

fail=0
for p in "$ROOT"/policies/*.yml; do
	if "$BIN" policy validate "$p" >/dev/null 2>&1; then echo "ok    $(basename "$p")"; else echo "FAIL  $(basename "$p")"; fail=1; fi
done
exit $fail
