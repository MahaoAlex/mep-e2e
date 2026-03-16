#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STRATEGY_FILE="${SCRIPT_DIR}/agw-e2e.yaml"
LOG_DIR="${SCRIPT_DIR}/logs"
REPORT_DIR="${SCRIPT_DIR}/reports"

PARALLEL_WORKERS=4
LOG_LEVEL="debug"
CLEANUP_ONLY=false
CLEANUP_IMAGES=false
TARGET_CASE=""
RUN_ALL=true

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

declare -a SERVER_CONTAINERS

# Check yq availability once
YQ_AVAILABLE=false
command -v yq &> /dev/null && YQ_AVAILABLE=true

# =============================================================================
# Logging
# =============================================================================

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# =============================================================================
# CLI
# =============================================================================

show_usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Options:
    --case <id>         Run specific test case (e.g., E2E-001)
    --all               Run all test cases (default)
    --parallel <num>    Number of parallel workers (default: 4)
    --cleanup           Cleanup residual containers/networks
    --cleanup --images  Additionally remove built images
    --log-level         Logging level: debug, info, warn, error
    -h, --help          Show this help message

Examples:
    $(basename "$0") --case E2E-001
    $(basename "$0") --all --parallel 8
    $(basename "$0") --cleanup --images
EOF
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --case)        TARGET_CASE="$2"; RUN_ALL=false; shift 2 ;;
            --all)         RUN_ALL=true; shift ;;
            --parallel)    PARALLEL_WORKERS="$2"; shift 2 ;;
            --cleanup)     CLEANUP_ONLY=true; shift ;;
            --images)      CLEANUP_IMAGES=true; shift ;;
            --log-level)   LOG_LEVEL="$2"; shift 2 ;;
            -h|--help)     show_usage; exit 0 ;;
            *)             log_error "Unknown option: $1"; show_usage; exit 1 ;;
        esac
    done
}

# =============================================================================
# YAML Helpers (yq-first with grep fallback)
# =============================================================================

yq_get() {
    local query="$1"
    [ "$YQ_AVAILABLE" = true ] && yq e "$query" "$STRATEGY_FILE" && return
    echo ""
}

yq_get_case() {
    local case_id="$1"
    local query="$2"
    yq_get ".testCases[] | select(.id == \"$case_id\") | $query"
}

yq_get_endpoint() {
    local case_id="$1"
    local idx="$2"
    local query="$3"
    yq_get_case "$case_id" ".server.endpoints[$idx] | $query"
}

get_test_case_ids() {
    [ "$YQ_AVAILABLE" = true ] && yq_get '.testCases[].id' && return
    grep -E "^  - id:" "$STRATEGY_FILE" | sed 's/.*id: *"\([^"]*\)".*/\1/'
}

get_test_case_count() {
    [ "$YQ_AVAILABLE" = true ] && yq_get '.testCases | length' && return
    grep -cE "^  - id:" "$STRATEGY_FILE"
}

get_case_name()     { yq_get_case "$1" ".name"; }
get_case_enabled()  { yq_get_case "$1" ".enabled"; }
get_expected()      { yq_get_case "$1" ".expected.$2"; }
get_client_envs()   { yq_get_case "$1" ".client.env[]"; }
get_endpoint_count() { yq_get_case "$1" ".server.endpoints | length"; }

is_null_or_empty() {
    [ -z "$1" ] || [ "$1" = "null" ]
}

# =============================================================================
# Docker Operations
# =============================================================================

build_images() {
    log_info "Building Docker images..."
    
    docker build -t agw-e2e-server:latest "${SCRIPT_DIR}/../server" 2>&1 | tee -a "${LOG_DIR}/build.log" || {
        log_error "Failed to build server image"; exit 1
    }
    log_info "Server image built"
    
    docker build -t agw-e2e-client:latest "${SCRIPT_DIR}/../client" 2>&1 | tee -a "${LOG_DIR}/build.log" || {
        log_error "Failed to build client image"; exit 1
    }
    log_info "Client image built"
}

cleanup_containers() {
    local filter="$1"
    local rm_images="$2"
    
    log_info "Cleaning up..."
    
    if [ -n "$filter" ]; then
        docker rm -f "agw-e2e-server-${filter}-"* 2>/dev/null || true
        docker rm -f "agw-e2e-client-${filter}-"* 2>/dev/null || true
    else
        docker rm -f $(docker ps -aq --filter "name=agw-e2e-server") 2>/dev/null || true
        docker rm -f $(docker ps -aq --filter "name=agw-e2e-client") 2>/dev/null || true
    fi
    
    [ "$rm_images" = true ] && {
        docker rmi agw-e2e-server:latest agw-e2e-client:latest 2>/dev/null || true
    }
    
    docker network prune -f 2>/dev/null || true
    log_info "Cleanup done"
}

# =============================================================================
# Server Container Management
# =============================================================================

build_env_var() {
    local name="$1"
    local value="$2"
    is_null_or_empty "$value" && return
    echo "-e ${name}='${value}'"
}

parse_endpoint_env() {
    local case_id="$1"
    local idx="$2"
    local port="$3"
    
    local env="-e TEST_CASE_ID=${case_id} -e SERVER_PORT_1=${port}"
    
    local action=$(yq_get_endpoint "$case_id" "$idx" ".behavior.action")
    local resp_code=$(yq_get_endpoint "$case_id" "$idx" ".behavior.responseCode")
    local resp_body=$(yq_get_endpoint "$case_id" "$idx" ".behavior.responseBody")
    local delay=$(yq_get_endpoint "$case_id" "$idx" ".behavior.delay")
    local fail_count=$(yq_get_endpoint "$case_id" "$idx" ".behavior.count")
    local fail_code=$(yq_get_endpoint "$case_id" "$idx" ".behavior.failResponseCode")
    local fail_body=$(yq_get_endpoint "$case_id" "$idx" ".behavior.failResponseBody")
    local succ_code=$(yq_get_endpoint "$case_id" "$idx" ".behavior.successResponseCode")
    local succ_body=$(yq_get_endpoint "$case_id" "$idx" ".behavior.successResponseBody")
    
    env="$env $(build_env_var BEHAVIOR_ACTION "$action")"
    env="$env $(build_env_var RESPONSE_CODE "$resp_code")"
    env="$env $(build_env_var RESPONSE_BODY "$resp_body")"
    env="$env $(build_env_var DELAY "$delay")"
    env="$env $(build_env_var FAIL_COUNT "$fail_count")"
    env="$env $(build_env_var FAIL_RESPONSE_CODE "$fail_code")"
    env="$env $(build_env_var FAIL_RESPONSE_BODY "$fail_body")"
    env="$env $(build_env_var SUCCESS_RESPONSE_CODE "$succ_code")"
    env="$env $(build_env_var SUCCESS_RESPONSE_BODY "$succ_body")"
    
    echo "$env"
}

wait_for_health() {
    local container="$1"
    local port=$(docker port "$container" 8080 2>/dev/null | cut -d: -f2)
    
    for i in {1..10}; do
        curl -s "http://localhost:${port}/health" > /dev/null 2>&1 && return 0
        sleep 1
    done
    log_warn "Health check timeout: ${container}"
}

start_single_server() {
    local case_id="$1"
    local idx="$2"
    local port="$3"
    local network="$4"
    
    local container="agw-e2e-server-${case_id}-${idx}"
    local env=$(parse_endpoint_env "$case_id" "$idx" "$port")
    
    eval docker run -d --name "$container" --network "$network" -p "${port}:8080" $env agw-e2e-server:latest > /dev/null 2>&1 || {
        log_error "Failed to start: ${container}"
        return 1
    }
    
    SERVER_CONTAINERS+=("$container")
    log_info "Started server: ${container} on port ${port}"
}

start_server_containers() {
    local case_id="$1"
    local base_port="$2"
    
    local network="agw-e2e-net-${case_id}"
    local count=$(get_endpoint_count "$case_id")
    
    is_null_or_empty "$count" && { log_info "No endpoints for ${case_id}"; return; }
    [ "$count" = "0" ] && { log_info "No endpoints for ${case_id}"; return; }
    
    docker network create "$network" 2>/dev/null || true
    SERVER_CONTAINERS=()
    
    for ((i=0; i<count; i++)); do
        local port=$((base_port + i))
        start_single_server "$case_id" "$i" "$port" "$network"
    done
    
    sleep 2
    for container in "${SERVER_CONTAINERS[@]}"; do
        wait_for_health "$container"
    done
    
    echo "$network"
}

stop_server_containers() {
    local network="$1"
    
    for c in "${SERVER_CONTAINERS[@]}"; do
        docker rm -f "$c" 2>/dev/null || true
    done
    [ -n "$network" ] && docker network rm "$network" 2>/dev/null || true
}

# =============================================================================
# Client Execution
# =============================================================================

build_callback_addrs() {
    local addrs=""
    for c in "${SERVER_CONTAINERS[@]}"; do
        local port=$(docker port "$c" 8080 2>/dev/null | cut -d: -f2)
        addrs="${addrs:+$addrs,}localhost:$port"
    done
    echo "$addrs"
}

run_client() {
    local case_id="$1"
    local client_env="$2"
    local callback="$3"
    
    local container="agw-e2e-client-${case_id}"
    
    if [ -n "$callback" ]; then
        docker run --rm --name "$container" $client_env -e "CALLBACK_ADDRS=$callback" agw-e2e-client:latest 2>&1
    else
        docker run --rm --name "$container" $client_env agw-e2e-client:latest 2>&1
    fi
}

# =============================================================================
# Test Execution
# =============================================================================

run_test_case() {
    local case_id="$1"
    local base_port="$2"
    
    local name=$(get_case_name "$case_id")
    local enabled=$(get_case_enabled "$case_id")
    
    [ "$enabled" = "false" ] && { log_info "Skip disabled: ${case_id}"; echo "SKIP::"; return 0; }
    
    log_info "=== Running: ${case_id} - ${name} ==="
    
    local network=$(start_server_containers "$case_id" "$base_port")
    
    local http_code=$(get_expected "$case_id" "httpCode")
    local resp_code=$(get_expected "$case_id" "responseCode")
    local resp_msg=$(get_expected "$case_id" "responseMsg")
    local err_contains=$(get_expected "$case_id" "errorContains")
    
    local client_env="-e TEST_CASE_ID=$case_id"
    is_null_or_empty "$http_code"    || client_env="$client_env -e EXPECTED_HTTP_CODE=$http_code"
    is_null_or_empty "$resp_code"    || client_env="$client_env -e EXPECTED_RESPONSE_CODE=$resp_code"
    is_null_or_empty "$resp_msg"     || client_env="$client_env -e EXPECTED_MSG='$resp_msg'"
    is_null_or_empty "$err_contains" || client_env="$client_env -e EXPECTED_ERROR='$err_contains'"
    
    while IFS= read -r env_line; do
        is_null_or_empty "$env_line" || client_env="$client_env -e $env_line"
    done <<< "$(get_client_envs "$case_id")"
    
    local callback=""
    [ ${#SERVER_CONTAINERS[@]} -gt 0 ] && callback=$(build_callback_addrs)
    
    local output exit_code=0
    output=$(run_client "$case_id" "$client_env" "$callback") || exit_code=$?
    
    {
        echo "=== ${case_id}: ${name} ==="
        echo "Servers: ${SERVER_CONTAINERS[*]}"
        echo "Output:"
        echo "$output"
        echo "Exit: ${exit_code}"
    } > "${LOG_DIR}/${case_id}.log"
    
    stop_server_containers "$network"
    
    [ $exit_code -eq 0 ] && { log_info "PASS: ${case_id}"; echo "PASS:${case_id}:0"; } \
                       || { log_error "FAIL: ${case_id}"; echo "FAIL:${case_id}:${exit_code}"; }
}

run_tests_sequential() {
    local ids="$1"
    local port=8000 offset=0
    
    readarray -t cases <<< "$ids"
    for id in "${cases[@]}"; do
        run_test_case "$id" $((port + offset * 100))
        ((offset++))
    done > "${LOG_DIR}/results.txt"
}

run_tests_parallel() {
    local ids="$1"
    local workers="$2"
    local port=8000 offset=0 running=0
    local -a pids=()
    
    readarray -t cases <<< "$ids"
    
    for id in "${cases[@]}"; do
        while [ $running -ge $workers ]; do
            for i in "${!pids[@]}"; do
                kill -0 "${pids[$i]}" 2>/dev/null || {
                    wait "${pids[$i]}" 2>/dev/null
                    unset 'pids[$i]'
                    ((running--))
                    break
                }
            done
            sleep 0.5
        done
        
        (
            run_test_case "$id" $((port + offset * 100))
        ) >> "${LOG_DIR}/results.txt" &
        pids+=($!)
        ((running++))
        ((offset++))
    done
    
    for p in "${pids[@]}"; do wait "$p" 2>/dev/null; done
}

# =============================================================================
# Report
# =============================================================================

count_results() {
    local passed=0 failed=0 skipped=0
    
    [ -f "${LOG_DIR}/results.txt" ] || return
    
    while IFS=: read -r result _ _; do
        case "$result" in
            PASS) ((passed++)) ;;
            FAIL) ((failed++)) ;;
            SKIP) ((skipped++)) ;;
        esac
    done < "${LOG_DIR}/results.txt"
    
    echo "${passed} ${failed} ${skipped}"
}

generate_report() {
    local report="${REPORT_DIR}/report_$(date +%Y%m%d_%H%M%S).md"
    local counts=$(count_results)
    local passed=$(echo $counts | cut -d' ' -f1)
    local failed=$(echo $counts | cut -d' ' -f2)
    local skipped=$(echo $counts | cut -d' ' -f3)
    local total=$((passed + failed + skipped))
    
    {
        echo "# AGW E2E Test Report"
        echo "**Generated:** $(date)"
        echo ""
        echo "| Status | Count |"
        echo "|--------|-------|"
        echo "| Total | $total |"
        echo "| Passed | $passed |"
        echo "| Failed | $failed |"
        echo "| Skipped | $skipped |"
        echo ""
        echo "## Details"
        echo "| Test ID | Result | Exit Code |"
        echo "|---------|--------|-----------|"
        
        while IFS=: read -r result id code; do
            local icon="✅"
            [ "$result" = "FAIL" ] && icon="❌"
            [ "$result" = "SKIP" ] && icon="⏭️"
            echo "| $id | $icon $result | ${code:-0} |"
        done < "${LOG_DIR}/results.txt"
        
        echo ""
        echo "Logs: \`${LOG_DIR}\`"
    } > "$report"
    
    echo "$report"
}

print_summary() {
    local counts=$(count_results)
    local passed=$(echo $counts | cut -d' ' -f1)
    local failed=$(echo $counts | cut -d' ' -f2)
    local skipped=$(echo $counts | cut -d' ' -f3)
    
    log_info "Summary: ${passed} passed, ${failed} failed, ${skipped} skipped"
    [ "$failed" -gt 0 ] && { log_error "Tests failed"; return 1; }
}

# =============================================================================
# Main
# =============================================================================

main() {
    parse_args "$@"
    mkdir -p "${LOG_DIR}" "${REPORT_DIR}"
    rm -f "${LOG_DIR}/results.txt"
    
    [ "$CLEANUP_ONLY" = true ] && { cleanup_containers "$TARGET_CASE" "$CLEANUP_IMAGES"; exit 0; }
    
    [ ! -f "$STRATEGY_FILE" ] && { log_error "Strategy file not found: ${STRATEGY_FILE}"; exit 1; }
    
    [ "$YQ_AVAILABLE" = false ] && log_warn "yq not found, using fallback parsing"
    
    build_images
    
    log_info "Testing: ${STRATEGY_FILE}"
    log_info "Workers: ${PARALLEL_WORKERS}"
    
    local test_ids test_count
    if [ "$RUN_ALL" = true ]; then
        test_ids=$(get_test_case_ids)
        test_count=$(get_test_case_count)
    else
        [ -z "$TARGET_CASE" ] && { log_error "No test case specified"; show_usage; exit 1; }
        test_ids="$TARGET_CASE"
        test_count=1
    fi
    
    log_info "Running ${test_count} test(s)"
    
    local start=$(date +%s)
    
    [ "$PARALLEL_WORKERS" -gt 1 ] && [ "$RUN_ALL" = true ] \
        && run_tests_parallel "$test_ids" "$PARALLEL_WORKERS" \
        || run_tests_sequential "$test_ids"
    
    local duration=$(($(date +%s) - start))
    
    local report=$(generate_report)
    
    log_info "Done in ${duration}s | Logs: ${LOG_DIR} | Report: ${report}"
    
    print_summary
}

main "$@"
