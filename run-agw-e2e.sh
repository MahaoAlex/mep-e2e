#!/bin/bash

set -e

# =============================================================================
# AGW E2E Test Framework - Main Entry Script
# =============================================================================

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STRATEGY_FILE="${SCRIPT_DIR}/agw-e2e.yaml"
LOG_DIR="${SCRIPT_DIR}/logs"
REPORT_DIR="${SCRIPT_DIR}/reports"

# Default values
PARALLEL_WORKERS=4
LOG_LEVEL="debug"
CLEANUP_ONLY=false
CLEANUP_IMAGES=false

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# =============================================================================
# Helper Functions
# =============================================================================

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

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
    $(basename "$0") --all
    $(basename "$0") --all --parallel 8
    $(basename "$0") --cleanup
    $(basename "$0") --cleanup --images
EOF
}

# =============================================================================
# Parse Arguments
# =============================================================================

parse_args() {
    TARGET_CASE=""
    RUN_ALL=true
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --case)
                TARGET_CASE="$2"
                RUN_ALL=false
                shift 2
                ;;
            --all)
                RUN_ALL=true
                shift
                ;;
            --parallel)
                PARALLEL_WORKERS="$2"
                shift 2
                ;;
            --cleanup)
                CLEANUP_ONLY=true
                shift
                ;;
            --images)
                CLEANUP_IMAGES=true
                shift
                ;;
            --log-level)
                LOG_LEVEL="$2"
                shift 2
                ;;
            -h|--help)
                show_usage
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
}

# =============================================================================
# Docker Image Management
# =============================================================================

build_images() {
    log_info "Building Docker images..."
    
    # Build server image
    if docker build -t agw-e2e-server:latest "${SCRIPT_DIR}/server" 2>&1 | tee -a "${LOG_DIR}/build.log"; then
        log_info "Server image built successfully"
    else
        log_error "Failed to build server image"
        exit 1
    fi
    
    # Build client image
    if docker build -t agw-e2e-client:latest "${SCRIPT_DIR}/client" 2>&1 | tee -a "${LOG_DIR}/build.log"; then
        log_info "Client image built successfully"
    else
        log_error "Failed to build client image"
        exit 1
    fi
}

# =============================================================================
# Container Cleanup
# =============================================================================

cleanup_containers() {
    local case_filter="$1"
    local remove_images="$2"
    
    log_info "Cleaning up containers..."
    
    # Remove server containers
    if [ -n "$case_filter" ]; then
        docker rm -f "agw-e2e-server-${case_filter}-"* 2>/dev/null || true
        docker rm -f "agw-e2e-client-${case_filter}-"* 2>/dev/null || true
    else
        docker rm -f $(docker ps -aq --filter "name=agw-e2e-server") 2>/dev/null || true
        docker rm -f $(docker ps -aq --filter "name=agw-e2e-client") 2>/dev/null || true
    fi
    
    # Remove images if requested
    if [ "$remove_images" = true ]; then
        log_info "Removing Docker images..."
        docker rmi agw-e2e-server:latest 2>/dev/null || true
        docker rmi agw-e2e-client:latest 2>/dev/null || true
    fi
    
    # Cleanup networks
    docker network prune -f 2>/dev/null || true
    
    log_info "Cleanup completed"
}

# =============================================================================
# Test Case Execution
# =============================================================================

run_single_test() {
    local case_id="$1"
    local worker_id="$2"
    local callback_addrs="$3"
    local expected_http_code="$4"
    local expected_response_code="$5"
    local expected_msg="$6"
    local expected_error="$7"
    
    # Calculate unique port for this worker
    local base_port=8000
    local server_port=$((base_port + worker_id * 2))
    local server_port_2=$((base_port + worker_id * 2 + 1))
    
    # Start server container
    local server_container="agw-e2e-server-${case_id}-${worker_id}"
    docker run -d --name "$server_container" \
        -p "${server_port}:8080" \
        -p "${server_port_2}:8081" \
        -e TEST_CASE_ID="${case_id}" \
        -e SERVER_PORT_1="${server_port}" \
        -e SERVER_PORT_2="${server_port_2}" \
        agw-e2e-server:latest > /dev/null 2>&1
    
    # Wait for server to be ready
    sleep 2
    
    # Run client test
    local client_container="agw-e2e-client-${case_id}-${worker_id}"
    local test_output
    test_output=$(docker run --rm \
        --name "$client_container" \
        -e TEST_CASE_ID="${case_id}" \
        -e CALLBACK_ADDRS="localhost:${server_port},localhost:${server_port_2}" \
        -e EXPECTED_HTTP_CODE="${expected_http_code}" \
        -e EXPECTED_RESPONSE_CODE="${expected_response_code}" \
        -e EXPECTED_MSG="${expected_msg}" \
        -e EXPECTED_ERROR="${expected_error}" \
        agw-e2e-client:latest 2>&1) || true
    
    # Capture exit code
    local exit_code=$?
    
    # Collect logs
    local log_file="${LOG_DIR}/${case_id}-${worker_id}.log"
    echo "$test_output" > "$log_file"
    
    # Cleanup containers
    docker rm -f "$server_container" 2>/dev/null || true
    docker rm -f "$client_container" 2>/dev/null || true
    
    # Determine pass/fail
    if [ $exit_code -eq 0 ]; then
        echo "PASS"
    else
        echo "FAIL: $test_output"
        exit 1
    fi
}

# =============================================================================
# Main Execution
# =============================================================================

main() {
    parse_args "$@"
    
    # Create directories
    mkdir -p "${LOG_DIR}" "${REPORT_DIR}"
    
    # Handle cleanup mode
    if [ "$CLEANUP_ONLY" = true ]; then
        cleanup_containers "$TARGET_CASE" "$CLEANUP_IMAGES"
        exit 0
    fi
    
    # Build images
    build_images
    
    log_info "Starting E2E test execution..."
    log_info "Parallel workers: ${PARALLEL_WORKERS}"
    
    # TODO: Parse agw-e2e.yaml and execute test cases
    # This is a simplified version - full implementation would parse YAML
    
    log_info "Test execution completed"
    log_info "Logs available at: ${LOG_DIR}"
    log_info "Reports available at: ${REPORT_DIR}"
}

# Run main function
main "$@"