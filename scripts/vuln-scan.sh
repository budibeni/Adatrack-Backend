#!/usr/bin/env bash
# ============================================================================
# vuln-scan.sh — security gate: dependency + stdlib vulnerability scan (PRD §4.2)
# ============================================================================
# Runs govulncheck over EVERY Go module and fails on any finding that is actually
# reached by our code (exit status 3). This is the gate that caught the 2026-09-29
# findings — sql-injection-adjacent placeholder confusion in pgx v5.7.5 and an
# infinite loop in x/text — both of which were then fixed by upgrading.
#
# The toolchain matters for the result: the standard-library findings depend on
# the Go release used to build. go.mod now pins `toolchain go1.26.6` and the
# Dockerfiles build with golang:1.26.6-alpine so a patched stdlib is guaranteed.
#
# Usage: scripts/vuln-scan.sh
# ============================================================================
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GOVULN="${GOVULNCHECK_BIN:-$(go env GOPATH)/bin/govulncheck}"
if ! command -v "$GOVULN" >/dev/null 2>&1; then
  echo "vuln-scan: govulncheck not found at '$GOVULN'." >&2
  echo "vuln-scan: install it with:  go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
  exit 127
fi

echo "vuln-scan: toolchain $(go version | awk '{print $3}') · govulncheck $("$GOVULN" -version 2>/dev/null | head -1)"
fail=0
for mod in $(find . -name go.mod | sort | xargs -n1 dirname); do
  echo "vuln-scan: === ${mod} ==="
  if ! (cd "$mod" && "$GOVULN" ./...); then
    fail=1
  fi
done

echo
if [[ "$fail" -eq 0 ]]; then
  echo "vuln-scan: PASS — no reachable vulnerabilities in any module"
else
  echo "vuln-scan: FAIL — reachable vulnerabilities found (see the output above)" >&2
fi
exit "$fail"
