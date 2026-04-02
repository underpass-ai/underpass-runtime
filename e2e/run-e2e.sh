#!/usr/bin/env bash
set -euo pipefail

# ─── UnderPass Runtime — Unified E2E Runner ─────────────────────────────────
#
# Builds one image, deploys per-test Jobs from a template, waits, reports.
#
# Usage:
#   ./e2e/run-e2e.sh [OPTIONS]
#
# Options:
#   --tier <smoke|core|full|all>  Filter by tier (default: all)
#   --test <id>                   Single test by ID (e.g., 04, 16)
#   --skip-build                  Reuse existing image
#   --skip-push                   Don't push to registry
#   --cleanup                     Delete jobs after run
#   --image <img>                 Override image (default: build & tag)
#   --namespace <ns>              K8s namespace (default: underpass-runtime)
# ─────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEMPLATE="${SCRIPT_DIR}/job-template.yaml"
CATALOG="${SCRIPT_DIR}/tests/e2e_tests.yaml"

BUILDER="$(command -v podman 2>/dev/null || command -v docker 2>/dev/null)"
NAMESPACE="underpass-runtime"
REGISTRY="ghcr.io/underpass-ai/underpass-runtime"
IMAGE_TAG="e2e-latest"
IMAGE=""
TIER="all"
SINGLE_TEST=""
SKIP_BUILD=false
SKIP_PUSH=false
CLEANUP=false

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
PASSED=0; FAILED=0; SKIPPED=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tier)       TIER="$2"; shift 2 ;;
    --test)       SINGLE_TEST="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --skip-push)  SKIP_PUSH=true; shift ;;
    --cleanup)    CLEANUP=true; shift ;;
    --image)      IMAGE="$2"; shift 2 ;;
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    --help)       head -18 "$0" | tail -14; exit 0 ;;
    *) echo -e "${RED}Unknown: $1${NC}"; exit 1 ;;
  esac
done

# ─── Parse catalog ──────────────────────────────────────────────────────────

declare -A TEST_NAME TEST_JOB TEST_TIER TEST_TIMEOUT
while IFS= read -r line; do
  if [[ "$line" =~ ^[[:space:]]*-[[:space:]]*id:[[:space:]]*\"([^\"]+)\" ]]; then
    cid="${BASH_REMATCH[1]}"
  elif [[ "$line" =~ ^[[:space:]]*job_name:[[:space:]]*\"([^\"]+)\" ]]; then
    TEST_JOB[$cid]="${BASH_REMATCH[1]}"
  elif [[ "$line" =~ ^[[:space:]]*name:[[:space:]]*\"([^\"]+)\" ]]; then
    TEST_NAME[$cid]="${BASH_REMATCH[1]}"
  elif [[ "$line" =~ ^[[:space:]]*tier:[[:space:]]*\"([^\"]+)\" ]]; then
    TEST_TIER[$cid]="${BASH_REMATCH[1]}"
  elif [[ "$line" =~ ^[[:space:]]*timeout_override:[[:space:]]*([0-9]+) ]]; then
    TEST_TIMEOUT[$cid]="${BASH_REMATCH[1]}"
  fi
done < "$CATALOG"

# Filter tests.
TESTS=()
for tid in $(printf '%s\n' "${!TEST_NAME[@]}" | sort); do
  [[ -n "$SINGLE_TEST" && "$tid" != "$SINGLE_TEST" ]] && continue
  [[ "$TIER" != "all" && "${TEST_TIER[$tid]}" != "$TIER" ]] && continue
  # Skip tests that require special infra (LLM, vLLM, tool-learning Go binary).
  [[ "$tid" == "10" || "$tid" == "11" || "$tid" == "15" ]] && { SKIPPED=$((SKIPPED+1)); continue; }
  TESTS+=("$tid")
done

echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  UnderPass Runtime — E2E Suite (${#TESTS[@]} tests, tier=$TIER)${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"

# ─── Build ──────────────────────────────────────────────────────────────────

if [[ -z "$IMAGE" ]]; then
  IMAGE="${REGISTRY}/e2e-runner:${IMAGE_TAG}"
fi

if [[ "$SKIP_BUILD" == "false" ]]; then
  echo -e "${BLUE}Building unified E2E image...${NC}"
  ${BUILDER} build --no-cache -f "${SCRIPT_DIR}/Dockerfile" -t "$IMAGE" "$PROJECT_ROOT" 2>&1 | tail -3
fi

if [[ "$SKIP_PUSH" == "false" ]]; then
  echo -e "${BLUE}Pushing $IMAGE...${NC}"
  ${BUILDER} push "$IMAGE" 2>&1 | tail -2
fi

# ─── Deploy ─────────────────────────────────────────────────────────────────

echo ""
for tid in "${TESTS[@]}"; do
  job="${TEST_JOB[$tid]}"
  timeout="${TEST_TIMEOUT[$tid]:-600}"

  # Delete previous run.
  kubectl delete job "$job" -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null

  # Instantiate template.
  sed -e "s|__TEST_ID__|${tid}|g" \
      -e "s|__JOB_NAME__|${job}|g" \
      -e "s|__TIMEOUT__|${timeout}|g" \
      -e "s|__IMAGE__|${IMAGE}|g" \
      "$TEMPLATE" | kubectl apply -f - 2>/dev/null

  echo -e "  ${BLUE}▶${NC} ${TEST_NAME[$tid]} (${job}, ${timeout}s)"
done

# ─── Wait & Collect ─────────────────────────────────────────────────────────

echo -e "\n${BLUE}Waiting for jobs...${NC}"
MAX_WAIT=600
ELAPSED=0

while (( ELAPSED < MAX_WAIT )); do
  DONE=true
  for tid in "${TESTS[@]}"; do
    job="${TEST_JOB[$tid]}"
    status=$(kubectl get job "$job" -n "$NAMESPACE" -o jsonpath='{.status.conditions[0].type}' 2>/dev/null || echo "")
    [[ -z "$status" ]] && DONE=false
  done
  $DONE && break
  sleep 5
  ((ELAPSED+=5))
  printf "\r  %ds elapsed..." "$ELAPSED"
done
echo ""

# ─── Results ────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}═══════════════════════════════════════════════════════════${NC}"
printf "  ${BLUE}%-36s %-10s${NC}\n" "TEST" "RESULT"
echo -e "${BLUE}───────────────────────────────────────────────────────────${NC}"

for tid in "${TESTS[@]}"; do
  job="${TEST_JOB[$tid]}"
  name="${TEST_NAME[$tid]}"
  succeeded=$(kubectl get job "$job" -n "$NAMESPACE" -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "")
  failed=$(kubectl get job "$job" -n "$NAMESPACE" -o jsonpath='{.status.failed}' 2>/dev/null || echo "")

  if [[ "$succeeded" == "1" ]]; then
    printf "  %-36s ${GREEN}PASSED${NC}\n" "$name"
    PASSED=$((PASSED+1))
  elif [[ -n "$failed" && "$failed" != "0" ]]; then
    printf "  %-36s ${RED}FAILED${NC}\n" "$name"
    FAILED=$((FAILED+1))
  else
    printf "  %-36s ${YELLOW}TIMEOUT${NC}\n" "$name"
    FAILED=$((FAILED+1))
  fi
done

echo -e "${BLUE}───────────────────────────────────────────────────────────${NC}"
echo -e "  ${GREEN}Passed: $PASSED${NC}  ${RED}Failed: $FAILED${NC}  ${YELLOW}Skipped: $SKIPPED${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"

# ─── Cleanup ────────────────────────────────────────────────────────────────

if [[ "$CLEANUP" == "true" ]]; then
  kubectl delete jobs -n "$NAMESPACE" -l test-type=e2e --ignore-not-found=true 2>/dev/null
fi

[[ "$FAILED" -eq 0 ]] && exit 0 || exit 1
