#!/usr/bin/env bash
# quality-gate.sh — Local quality gate for underpass-runtime.
# Mirrors CI checks so developers catch issues before push.
#
# Usage:
#   bash scripts/ci/quality-gate.sh          # run all checks
#   bash scripts/ci/quality-gate.sh --quick  # skip coverage gate (faster)

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

QUICK=false
if [[ "${1:-}" == "--quick" ]]; then
  QUICK=true
fi

FAILED=0

step() {
  echo -e "\n${YELLOW}── $1${NC}"
}

pass() {
  echo -e "${GREEN}OK${NC} $1"
}

fail() {
  echo -e "${RED}FAIL${NC} $1"
  FAILED=1
}

# ── Format check ──
step "go fmt"
UNFMT=$(gofmt -l . 2>&1 | grep -v vendor/ | grep -v testdata/ || true)
if [[ -z "$UNFMT" ]]; then
  pass "all files formatted"
else
  fail "unformatted files:\n$UNFMT"
fi

# ── Vet ──
step "go vet"
if go vet ./... 2>&1; then
  pass "go vet (default)"
else
  fail "go vet (default)"
fi

if go vet -tags k8s ./... 2>&1; then
  pass "go vet (k8s)"
else
  fail "go vet (k8s)"
fi

# ── Lint ──
step "golangci-lint"
if command -v golangci-lint &>/dev/null; then
  if golangci-lint run --timeout=5m 2>&1; then
    pass "golangci-lint (default)"
  else
    fail "golangci-lint (default)"
  fi

  if golangci-lint run --timeout=5m --build-tags=k8s 2>&1; then
    pass "golangci-lint (k8s)"
  else
    fail "golangci-lint (k8s)"
  fi
else
  echo -e "${YELLOW}SKIP${NC} golangci-lint not installed"
fi

# ── Build ──
step "build"
if CGO_ENABLED=0 go build -o /dev/null ./cmd/workspace 2>&1; then
  pass "build (default)"
else
  fail "build (default)"
fi

if CGO_ENABLED=0 go build -tags k8s -o /dev/null ./cmd/workspace 2>&1; then
  pass "build (k8s)"
else
  fail "build (k8s)"
fi

# ── Tests ──
step "tests"
if go test -race -count=1 ./... 2>&1; then
  pass "unit tests (default)"
else
  fail "unit tests (default)"
fi

if go test -race -count=1 -tags k8s ./... 2>&1; then
  pass "unit tests (k8s)"
else
  fail "unit tests (k8s)"
fi

# ── Coverage gate ──
if [[ "$QUICK" == false ]]; then
  step "coverage gate (80% core)"
  CORE_PKGS="./internal/app ./internal/adapters/audit ./internal/adapters/policy ./internal/adapters/sessionstore ./internal/adapters/invocationstore"
  COVERAGE_FILE=$(mktemp)
  if go test $CORE_PKGS -coverprofile="$COVERAGE_FILE" -covermode=atomic 2>&1; then
    COV=$(go tool cover -func="$COVERAGE_FILE" | awk '/^total:/ {print $3}' | sed 's/%//')
    if awk "BEGIN {exit !($COV >= 80)}"; then
      pass "core coverage: ${COV}% >= 80%"
    else
      fail "core coverage: ${COV}% < 80%"
    fi
  else
    fail "coverage tests failed"
  fi
  rm -f "$COVERAGE_FILE"
else
  echo -e "\n${YELLOW}SKIP${NC} coverage gate (--quick mode)"
fi

# ── Security (optional) ──
step "govulncheck"
if command -v govulncheck &>/dev/null; then
  if govulncheck ./... 2>&1; then
    pass "no known vulnerabilities"
  else
    echo -e "${YELLOW}WARN${NC} govulncheck found issues (non-blocking)"
  fi
else
  echo -e "${YELLOW}SKIP${NC} govulncheck not installed"
fi

# ── Summary ──
echo ""
if [[ $FAILED -eq 0 ]]; then
  echo -e "${GREEN}All quality gates passed.${NC}"
  exit 0
else
  echo -e "${RED}Quality gate FAILED. Fix issues above before pushing.${NC}"
  exit 1
fi
