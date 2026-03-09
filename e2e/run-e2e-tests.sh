#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# UnderPass Runtime — E2E Test Runner
#
# Builds, pushes, deploys and monitors E2E test jobs on Kubernetes.
#
# Usage:
#   ./run-e2e-tests.sh [OPTIONS]
#
# Options:
#   --tier <smoke|core|full|all>   Filter tests by tier (default: all)
#   --test <id>                    Run a single test by ID (e.g., 01)
#   --skip-build                   Skip building container images
#   --skip-push                    Skip pushing images to registry
#   --cleanup                      Delete jobs after completion
#   --namespace <ns>               K8s namespace (default: underpass-runtime)
#   --registry <reg>               Container registry (default: registry.underpassai.com/underpass-runtime)
#   --timeout <secs>               Default per-test timeout (default: 600)
#   --evidence-dir <dir>           Local dir for evidence files (default: e2e/evidence)
#   --help                         Show this help
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TESTS_DIR="${SCRIPT_DIR}/tests"
CATALOG="${TESTS_DIR}/e2e_tests.yaml"

# Defaults
TIER="all"
SINGLE_TEST=""
SKIP_BUILD=false
SKIP_PUSH=false
CLEANUP=false
NAMESPACE="underpass-runtime"
REGISTRY="registry.underpassai.com/underpass-runtime"
DEFAULT_TIMEOUT=600
EVIDENCE_DIR="${SCRIPT_DIR}/evidence"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Counters
PASSED=0
FAILED=0
SKIPPED=0

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --tier)       TIER="$2"; shift 2 ;;
        --test)       SINGLE_TEST="$2"; shift 2 ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --skip-push)  SKIP_PUSH=true; shift ;;
        --cleanup)    CLEANUP=true; shift ;;
        --namespace)  NAMESPACE="$2"; shift 2 ;;
        --registry)   REGISTRY="$2"; shift 2 ;;
        --timeout)    DEFAULT_TIMEOUT="$2"; shift 2 ;;
        --evidence-dir) EVIDENCE_DIR="$2"; shift 2 ;;
        --help)
            head -25 "$0" | tail -20
            exit 0
            ;;
        *) echo -e "${RED}Unknown option: $1${NC}"; exit 1 ;;
    esac
done

mkdir -p "${EVIDENCE_DIR}"

# ---------------------------------------------------------------------------
# Parse YAML catalog (simple awk-based parser)
# ---------------------------------------------------------------------------
declare -a TEST_IDS=()
declare -A TEST_NAMES=()
declare -A TEST_JOBS=()
declare -A TEST_TIERS=()
declare -A TEST_TIMEOUTS=()

parse_catalog() {
    local current_id=""
    while IFS= read -r line; do
        line="${line#"${line%%[![:space:]]*}"}"  # trim leading whitespace
        case "$line" in
            "- id:"*)
                current_id="${line#*\"}"
                current_id="${current_id%%\"*}"
                TEST_IDS+=("$current_id")
                ;;
            "name:"*)
                local val="${line#*\"}"
                val="${val%%\"*}"
                TEST_NAMES["$current_id"]="$val"
                ;;
            "job_name:"*)
                local val="${line#*\"}"
                val="${val%%\"*}"
                TEST_JOBS["$current_id"]="$val"
                ;;
            "tier:"*)
                local val="${line#*\"}"
                val="${val%%\"*}"
                TEST_TIERS["$current_id"]="$val"
                ;;
            "timeout_override:"*)
                local val="${line#*: }"
                TEST_TIMEOUTS["$current_id"]="$val"
                ;;
        esac
    done < "$CATALOG"
}

parse_catalog

# ---------------------------------------------------------------------------
# Filter tests
# ---------------------------------------------------------------------------
declare -a RUN_TESTS=()

for tid in "${TEST_IDS[@]}"; do
    if [[ -n "$SINGLE_TEST" && "$tid" != "$SINGLE_TEST" ]]; then
        continue
    fi
    if [[ "$TIER" != "all" ]]; then
        test_tier="${TEST_TIERS[$tid]:-core}"
        case "$TIER" in
            smoke) [[ "$test_tier" != "smoke" ]] && continue ;;
            core)  [[ "$test_tier" != "smoke" && "$test_tier" != "core" ]] && continue ;;
            full)  ;; # run everything
        esac
    fi
    RUN_TESTS+=("$tid")
done

if [[ ${#RUN_TESTS[@]} -eq 0 ]]; then
    echo -e "${YELLOW}No tests match the filter (tier=${TIER}, test=${SINGLE_TEST:-all})${NC}"
    exit 0
fi

echo -e "${BLUE}================================================================${NC}"
echo -e "${BLUE}UnderPass Runtime E2E Test Suite${NC}"
echo -e "${BLUE}================================================================${NC}"
echo -e "  Namespace:  ${NAMESPACE}"
echo -e "  Registry:   ${REGISTRY}"
echo -e "  Tier:       ${TIER}"
echo -e "  Tests:      ${#RUN_TESTS[@]}"
echo -e "${BLUE}================================================================${NC}"
echo ""

# ---------------------------------------------------------------------------
# Build & push
# ---------------------------------------------------------------------------
build_test() {
    local tid="$1"
    local name="${TEST_NAMES[$tid]}"
    local test_dir="${TESTS_DIR}/${name}"

    if [[ ! -d "$test_dir" ]]; then
        echo -e "${RED}Test directory not found: ${test_dir}${NC}"
        return 1
    fi

    if [[ "$SKIP_BUILD" == "false" ]]; then
        echo -e "${BLUE}Building ${name}...${NC}"
        make -C "$test_dir" build REGISTRY="$REGISTRY" 2>&1 | tail -2
    fi

    if [[ "$SKIP_PUSH" == "false" ]]; then
        echo -e "${BLUE}Pushing ${name}...${NC}"
        make -C "$test_dir" push REGISTRY="$REGISTRY" 2>&1 | tail -2
    fi
}

# ---------------------------------------------------------------------------
# Deploy & monitor
# ---------------------------------------------------------------------------
deploy_test() {
    local tid="$1"
    local name="${TEST_NAMES[$tid]}"
    local job="${TEST_JOBS[$tid]}"
    local test_dir="${TESTS_DIR}/${name}"

    # Delete previous job if exists
    kubectl delete job -n "$NAMESPACE" "$job" --ignore-not-found=true 2>/dev/null

    echo -e "${BLUE}Deploying ${name}...${NC}"
    kubectl apply -f "${test_dir}/job.yaml" 2>&1
}

wait_for_test() {
    local tid="$1"
    local name="${TEST_NAMES[$tid]}"
    local job="${TEST_JOBS[$tid]}"
    local timeout="${TEST_TIMEOUTS[$tid]:-$DEFAULT_TIMEOUT}"
    local deadline=$((SECONDS + timeout))

    echo -e "${BLUE}Waiting for ${name} (timeout: ${timeout}s)...${NC}"

    while [[ $SECONDS -lt $deadline ]]; do
        local phase
        phase=$(kubectl get pods -n "$NAMESPACE" -l "app=${job}" \
            -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "Pending")

        case "$phase" in
            Succeeded)
                echo -e "${GREEN}PASSED ${name}${NC}"
                save_evidence "$tid" "passed"
                PASSED=$((PASSED + 1))
                return 0
                ;;
            Failed)
                echo -e "${RED}FAILED ${name}${NC}"
                save_evidence "$tid" "failed"
                FAILED=$((FAILED + 1))
                return 1
                ;;
        esac
        sleep 5
    done

    echo -e "${RED}TIMEOUT ${name} (${timeout}s)${NC}"
    save_evidence "$tid" "timeout"
    FAILED=$((FAILED + 1))
    return 1
}

# ---------------------------------------------------------------------------
# Evidence collection
# ---------------------------------------------------------------------------
save_evidence() {
    local tid="$1"
    local status="$2"
    local job="${TEST_JOBS[$tid]}"
    local name="${TEST_NAMES[$tid]}"
    local evidence_file="${EVIDENCE_DIR}/${name}-${status}.json"

    # Extract evidence JSON from pod logs
    local logs
    logs=$(kubectl logs -n "$NAMESPACE" -l "app=${job}" --tail=500 2>/dev/null || echo "")

    # Parse EVIDENCE_JSON_START/END block
    local evidence
    evidence=$(echo "$logs" | sed -n '/EVIDENCE_JSON_START/,/EVIDENCE_JSON_END/p' \
        | grep -v 'EVIDENCE_JSON_START\|EVIDENCE_JSON_END' || echo "")

    if [[ -n "$evidence" ]]; then
        echo "$evidence" > "$evidence_file"
        echo -e "  ${YELLOW}Evidence: ${evidence_file}${NC}"
    else
        # Save raw logs as fallback
        echo "$logs" > "${EVIDENCE_DIR}/${name}-${status}.log"
        echo -e "  ${YELLOW}Logs saved: ${EVIDENCE_DIR}/${name}-${status}.log${NC}"
    fi
}

# ---------------------------------------------------------------------------
# Main execution loop
# ---------------------------------------------------------------------------
for tid in "${RUN_TESTS[@]}"; do
    name="${TEST_NAMES[$tid]}"
    echo ""
    echo -e "${BLUE}────────────────────────────────────────────────────────────────${NC}"
    echo -e "${BLUE}Test ${tid}: ${name}${NC}"
    echo -e "${BLUE}────────────────────────────────────────────────────────────────${NC}"

    if ! build_test "$tid"; then
        echo -e "${RED}Build failed for ${name}, skipping${NC}"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi

    deploy_test "$tid"
    wait_for_test "$tid" || true

    if [[ "$CLEANUP" == "true" ]]; then
        kubectl delete job -n "$NAMESPACE" "${TEST_JOBS[$tid]}" --ignore-not-found=true 2>/dev/null
    fi
done

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo -e "${BLUE}================================================================${NC}"
echo -e "${BLUE}E2E Test Summary${NC}"
echo -e "${BLUE}================================================================${NC}"
echo -e "  ${GREEN}Passed:  ${PASSED}${NC}"
echo -e "  ${RED}Failed:  ${FAILED}${NC}"
echo -e "  ${YELLOW}Skipped: ${SKIPPED}${NC}"
echo -e "  Total:   ${#RUN_TESTS[@]}"
echo -e "${BLUE}================================================================${NC}"

if [[ $FAILED -gt 0 ]]; then
    exit 1
fi
exit 0
