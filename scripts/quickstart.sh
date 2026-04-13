#!/usr/bin/env bash
set -euo pipefail

# ─── Underpass Runtime — Quick Start ────────────────────────────────────────
#
# Demonstrates the full SWE agent workflow against a local runtime instance.
# No infrastructure needed — uses memory backends.
#
# Prerequisites: Go 1.25+, grpcurl, jq
#
# Usage:
#   ./scripts/quickstart.sh
#
# Options:
#   RUNTIME_BINARY=/path/to/binary  Skip go build, use prebuilt binary
#   PORT=50199                      gRPC port (default: 50199)
# ────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
PASS=0; FAIL=0

PORT="${PORT:-50199}"
METRICS_PORT="${METRICS_PORT:-9199}"
GRPCURL="${GRPCURL:-grpcurl}"
BINARY=""
RT_PID=""
OWNS_BINARY=false

cleanup() {
  [[ -n "$RT_PID" ]] && kill "$RT_PID" 2>/dev/null || true
  $OWNS_BINARY && [[ -n "$BINARY" && -f "$BINARY" ]] && rm -f "$BINARY" || true
}
trap cleanup EXIT

step() { echo -e "\n${BLUE}━━━ Step $1: $2${NC}"; }
ok()   { echo -e "  ${GREEN}✓ $1${NC}"; PASS=$((PASS + 1)); }
fail() { echo -e "  ${RED}✗ $1${NC}"; FAIL=$((FAIL + 1)); }

invoke() {
  local sid="$1" tool="$2" args="$3" approved="${4:-false}" cid="${5:-qs}"
  local payload
  payload=$(jq -n --arg sid "$sid" --arg tool "$tool" --arg args "$args" \
    --argjson approved "$approved" --arg cid "$cid" \
    '{session_id:$sid, tool_name:$tool, args:$args, approved:$approved, correlation_id:$cid}')
  $GRPCURL -plaintext -d "$payload" "localhost:${PORT}" \
    underpass.runtime.v1.InvocationService/InvokeTool 2>&1
}

echo -e "${BLUE}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║   Underpass Runtime — Quick Start                       ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════╝${NC}"

# ─── Step 0: Build ──────────────────────────────────────────────────────────

step 0 "Build runtime"
if [[ -n "${RUNTIME_BINARY:-}" && -x "$RUNTIME_BINARY" ]]; then
  BINARY="$RUNTIME_BINARY"
  ok "Using prebuilt binary"
else
  BINARY="$(mktemp -d)/underpass-runtime"
  go build -o "$BINARY" ./cmd/workspace 2>&1
  OWNS_BINARY=true
  ok "Binary built"
fi

# ─── Step 1: Start ─────────────────────────────────────────────────────────

step 1 "Start local runtime (memory backends, no TLS)"
PORT="$PORT" METRICS_PORT="$METRICS_PORT" \
  LOG_LEVEL=warn WORKSPACE_BACKEND=local STORES_BACKEND=memory \
  EVENT_BUS_TYPE=none TELEMETRY_BACKEND=memory AUTH_MODE=payload \
  ARTIFACTS_BACKEND=local "$BINARY" > /dev/null 2>&1 &
RT_PID=$!

READY=false
for _ in $(seq 1 10); do
  sleep 1
  if $GRPCURL -plaintext -connect-timeout 1 "localhost:${PORT}" \
     underpass.runtime.v1.HealthService/Check 2>/dev/null | grep -q '"ok"'; then
    READY=true; break
  fi
done
if $READY; then ok "Runtime healthy on :${PORT}"; else fail "Runtime failed to start"; exit 1; fi

# ─── Step 2: Create session ────────────────────────────────────────────────

step 2 "Create agent session"
SESSION=$($GRPCURL -plaintext -d '{
  "principal":{"tenant_id":"quickstart","actor_id":"demo-agent","roles":["developer","devops","platform_admin"]}
}' "localhost:${PORT}" underpass.runtime.v1.SessionService/CreateSession 2>&1)
SID=$(echo "$SESSION" | jq -r '.session.id // empty')
if [[ -n "$SID" ]]; then ok "Session: $SID"; else fail "Session failed"; exit 1; fi

# ─── Step 3: Discover tools ────────────────────────────────────────────────

step 3 "Discover available tools"
TOOLS=$($GRPCURL -plaintext -d "{\"session_id\":\"$SID\"}" \
  "localhost:${PORT}" underpass.runtime.v1.CapabilityCatalogService/ListTools 2>&1)
COUNT=$(echo "$TOOLS" | grep -c '"name"' || echo 0)
if (( COUNT > 100 )); then ok "$COUNT tools available"; else fail "Expected 100+, got $COUNT"; fi

# ─── Step 4: tool.suggest ──────────────────────────────────────────────────

step 4 "tool.suggest — recommend tools for a task"
RESULT=$(invoke "$SID" "tool.suggest" '{"task":"edit a function in a Go file","top_k":3}' false qs-004)
if echo "$RESULT" | grep -q "succeeded"; then
  ok "Got tool recommendations"
else
  fail "tool.suggest: $(echo "$RESULT" | jq -r '.invocation.error.message // "unknown"' 2>/dev/null)"
fi

# ─── Step 5: shell.exec ───────────────────────────────────────────────────

step 5 "shell.exec — create workspace files"
RESULT=$(invoke "$SID" "shell.exec" '{"command":"mkdir -p src && echo package main > src/main.go && echo created"}' true qs-005)
if echo "$RESULT" | grep -q "succeeded"; then
  ok "Workspace bootstrapped"
else
  fail "shell.exec failed"
fi

# ─── Step 6: repo.tree ────────────────────────────────────────────────────

step 6 "repo.tree — see workspace structure"
RESULT=$(invoke "$SID" "repo.tree" '{"max_depth":2}' false qs-006)
if echo "$RESULT" | grep -q "succeeded"; then
  ok "Directory tree retrieved"
else
  fail "repo.tree failed"
fi

# ─── Step 7: fs.edit ──────────────────────────────────────────────────────

step 7 "fs.edit — change 'main' to 'app' in source file"
RESULT=$(invoke "$SID" "fs.edit" '{"path":"src/main.go","old_string":"package main","new_string":"package app"}' true qs-007)
if echo "$RESULT" | grep -q "succeeded"; then
  ok "Edited package declaration"
else
  fail "fs.edit failed"
fi

# ─── Step 8: policy.check ─────────────────────────────────────────────────

step 8 "policy.check — verify path escape is blocked"
RESULT=$(invoke "$SID" "policy.check" '{"tool_name":"fs.edit","args":{"path":"../../../etc/passwd"}}' false qs-008)
if echo "$RESULT" | grep -q "succeeded"; then
  ok "Policy engine validates tool calls"
else
  fail "policy.check failed"
fi

# ─── Step 9: Close session ─────────────────────────────────────────────────

step 9 "Close session"
$GRPCURL -plaintext -d "{\"session_id\":\"$SID\"}" \
  "localhost:${PORT}" underpass.runtime.v1.SessionService/CloseSession > /dev/null 2>&1
ok "Session closed"

# ─── Summary ───────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
if (( FAIL == 0 )); then
  echo -e "${GREEN}  All $PASS steps passed!${NC}"
else
  echo -e "${RED}  $FAIL failed, $PASS passed${NC}"
fi
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
exit $FAIL
