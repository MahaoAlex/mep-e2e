# E2E Test Framework Proposal

## 1. Project Overview

This project aims to build a complete End-to-End (E2E) test framework for GatewayClient's multi-endpoint request functionality, verifying the correctness of RegisterSandbox and similar business operations under various scenarios.

## 2. Test Architecture

### 2.1 Overall Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        run-e2e.sh                               │
│                     (Test Entry Script)                         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Test Strategy File                           │
│                  (YAML Config File)                             │
└─────────────────────────────────────────────────────────────────┘
                              │
          ┌───────────────────┴───────────────────┐
          ▼                                       ▼
┌─────────────────────┐                 ┌─────────────────────┐
│   Client Container  │                 │  Server Container   │
│   (Business Code    │◄───────────────►│  (Multi-Endpoint    │
│    + Mock)          │                 │   Mock Service)     │
└─────────────────────┘                 └─────────────────────┘
```

### 2.2 Component Responsibilities

| Component | Responsibilities |
|-----------|------------------|
| `run-e2e.sh` | Parse strategy file, coordinate container lifecycle, execute tests, collect results |
| `strategy.yaml` | Define test cases, server behaviors, endpoint configurations |
| `client/` | Test client integrating business code, reusing existing AgentGatewayClient |
| `server/` | Multi-endpoint mock service, returning HTTP responses as configured |

## 3. Test Scenario Design

### 3.1 Scenario Matrix

| Scenario ID | Scenario Description | Expected Result |
|-------------|---------------------|-----------------|
| E2E-001 | First endpoint succeeds on first request | Returns 200 + correct response body |
| E2E-002 | First request fails, retry succeeds | Returns 200 + correct response body |
| E2E-003 | First endpoint fails twice, failover to second succeeds | Returns 200 + correct response body |
| E2E-004 | First returns 500, second returns 404 | Returns last received error response |
| E2E-005 | All endpoints timeout | Returns timeout error |
| E2E-006 | First returns 429, retry succeeds | Returns 200 + correct response body |
| E2E-007 | First returns 408, retry succeeds | Returns 200 + correct response body |
| E2E-008 | First returns 400, no retry | Returns 400 error |
| E2E-009 | First returns 401, no retry | Returns 401 error |
| E2E-010 | Client config error causes timeout, no retry | Returns timeout error |

### 3.2 Retry Logic Verification

Based on the business code implementation, the following retry rules need to be verified:

- **Retryable status codes**: `429`, `408`
- **Non-retryable status codes**: Other `4XX` errors (e.g., `400`, `401`, `403`, `404`)
- **Server errors**: `5XX` errors typically trigger retries until all endpoints are exhausted

## 3.3 Response Body Definition

According to README.md, the mock server needs to return the following response format:

### RegisterSandbox Success Response

```json
{
  "code": 0,
  "msg": "success",
  "request_id": "xxx-xxx-xxx"
}
```

### Error Response

```json
{
  "code": 500,
  "msg": "internal server error",
  "request_id": "xxx-xxx-xxx"
}
```

### UnregisterSandbox Request Format

```json
{
  "sandbox_id": "sandbox-123",
  "cell_id": "cell-456"
}
```

### Response Body Examples in Strategy File

```yaml
# Success response
responseBody: '{"code":0,"msg":"success","request_id":"test-001"}'

# Error response
responseBody: '{"code":503,"msg":"service unavailable","request_id":"test-001"}'
```

## 4. Strategy File Design

### 4.1 strategy.yaml Structure

```yaml
version: "1.0"

# Global config
config:
  timeout: 30s
  logLevel: debug

# Test case list
testCases:
  - id: "E2E-001"
    name: "First endpoint success on first try"
    description: "Verify first endpoint succeeds on first request"
    enabled: true
    
    # Client config
    client:
      image: "client:latest"
      env:
        - TEST_CASE_ID=E2E-001
        - GATEWAY_DOMAIN=server-1:8080
        - CALLBACK_ADDRS=server-1:8080
    
    # Server config
    server:
      endpoints:
        - address: "server-1:8080"
          port: 8080
          behavior:
            action: "success"
            responseBody: '{"code":0,"msg":"success","request_id":"test-001"}'
            responseCode: 200
            delay: 0
            failAfter: 0
```

### 4.2 Behavior Config

Server supports the following behavior types:

| Type | Description | Parameters |
|------|--------------|------------|
| `success` | Return success response | `responseCode`, `responseBody`, `delay` |
| `error` | Return error response | `responseCode`, `responseBody` |
| `timeout` | Simulate timeout | `delay` (exceeds client timeout) |
| `fail-after-n` | First N requests fail, then succeed | `count`, `failResponseCode` |

### 4.3 Complete Examples

```yaml
version: "1.0"

config:
  timeout: 60s

testCases:
  # E2E-001: First endpoint succeeds on first try
  - id: "E2E-001"
    name: "First endpoint success on first try"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-001
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "success"
            responseCode: 200
            responseBody: '{"code":0,"msg":"success","request_id":"test-001"}'

  # E2E-002: First endpoint fails then retries successfully
  - id: "E2E-002"
    name: "First endpoint fails then retries successfully"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-002
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "fail-after-n"
            count: 1
            failResponseCode: 503
            failResponseBody: '{"code":503,"msg":"service unavailable","request_id":"test-002"}'
            successResponseCode: 200
            successResponseBody: '{"code":0,"msg":"success","request_id":"test-002"}'

  # E2E-003: Failover to second endpoint succeeds
  - id: "E2E-003"
    name: "Failover to second endpoint succeeds"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-003
        - CALLBACK_ADDRS=mock-server-1:8080,mock-server-2:8081
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "fail-after-n"
            count: 2
            failResponseCode: 500
            failResponseBody: '{"code":500,"msg":"internal error","request_id":"test-003"}'
        - id: "mock-server-2"
          port: 8081
          behavior:
            action: "success"
            responseCode: 200
            responseBody: '{"code":0,"msg":"success","request_id":"test-003"}'

  # E2E-004: Both endpoints return errors
  - id: "E2E-004"
    name: "Both endpoints return errors"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-004
        - CALLBACK_ADDRS=mock-server-1:8080,mock-server-2:8081
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "error"
            responseCode: 500
            responseBody: '{"code":500,"msg":"internal server error","request_id":"test-004"}'
        - id: "mock-server-2"
          port: 8081
          behavior:
            action: "error"
            responseCode: 404
            responseBody: '{"code":404,"msg":"not found","request_id":"test-004"}'

  # E2E-005: All endpoints timeout
  - id: "E2E-005"
    name: "All endpoints timeout"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-005
        - CALLBACK_ADDRS=mock-server-1:8080,mock-server-2:8081
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "timeout"
            delay: 10000
        - id: "mock-server-2"
          port: 8081
          behavior:
            action: "timeout"
            delay: 10000

  # E2E-006: 429 retry then success
  - id: "E2E-006"
    name: "First endpoint returns 429 then retries successfully"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-006
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "fail-after-n"
            count: 1
            failResponseCode: 429
            failResponseBody: '{"code":429,"msg":"too many requests","request_id":"test-006"}'
            successResponseCode: 200
            successResponseBody: '{"code":0,"msg":"success","request_id":"test-006"}'

  # E2E-007: 408 retry then success
  - id: "E2E-007"
    name: "First endpoint returns 408 then retries successfully"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-007
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "fail-after-n"
            count: 1
            failResponseCode: 408
            failResponseBody: '{"code":408,"msg":"request timeout","request_id":"test-007"}'
            successResponseCode: 200
            successResponseBody: '{"code":0,"msg":"success","request_id":"test-007"}'

  # E2E-008: 400 no retry
  - id: "E2E-008"
    name: "First endpoint returns 400 no retry"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-008
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "error"
            responseCode: 400
            responseBody: '{"code":400,"msg":"bad request","request_id":"test-008"}'

  # E2E-009: 401 no retry
  - id: "E2E-009"
    name: "First endpoint returns 401 no retry"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-009
        - CALLBACK_ADDRS=mock-server-1:8080
    server:
      endpoints:
        - id: "mock-server-1"
          port: 8080
          behavior:
            action: "error"
            responseCode: 401
            responseBody: '{"code":401,"msg":"unauthorized","request_id":"test-009"}'

  # E2E-010: Client config error causes timeout
  - id: "E2E-010"
    name: "Client config error causes timeout no retry"
    enabled: true
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-010
        - CALLBACK_ADDRS=invalid-host:9999
        - CLIENT_ERROR=true
    server:
      endpoints: []
```

## 5. File Structure Design

```
mep-e2e/
├── README.md
├── run-e2e.sh                  # Main entry script
├── strategy.yaml              # Test strategy config
├── client/
│   ├── Dockerfile
│   ├── main.go                # Client entry
│   ├── config/
│   │   └── config.go
│   ├── client/
│   │   └── gateway_client.go  # AgentGatewayClient wrapper
│   ├── mock/
│   │   └── mock.go            # Business code mock
│   └── tests/
│       └── e2e_test.go        # Test case execution
└── server/
    ├── Dockerfile
    ├── main.go                # Server entry
    ├── handler/
    │   └── handler.go         # HTTP request handler
    ├── behavior/
    │   ├── behavior.go        # Behavior interface
    │   └── impl.go            # Behavior implementation
    └── config/
        └── config.go
```

## 6. Core Implementation

### 6.1 run-e2e.sh Script

**Responsibilities**:
1. Parse `strategy.yaml` config
2. Build and start server containers based on test case config
3. Build and start client containers
4. Collect test results
5. Cleanup resources

**Core Flow**:

```bash
#!/bin/bash

# 1. Parse strategy file
parse_strategy() { ... }

# 2. Start server
start_server() {
    docker build -t e2e-server:latest ./server
    docker run -d --name e2e-server-${case_id} \
        -p ${port}:${port} \
        -e BEHAVIOR_TYPE=${behavior} \
        -e RESPONSE_CODE=${response_code} \
        e2e-server:latest
}

# 3. Start client
start_client() {
    docker build -t e2e-client:latest ./client
    docker run --rm \
        -e TEST_CASE_ID=${case_id} \
        -e CALLBACK_ADDRS=${callback_addrs} \
        e2e-client:latest
}

# 4. Execute test and collect results
run_test() { ... }
```

### 6.2 Server Mock Implementation

**Endpoint Behavior Handling**:

```go
type BehaviorType string

const (
    BehaviorSuccess    BehaviorType = "success"
    BehaviorError      BehaviorType = "error"
    BehaviorTimeout    BehaviorType = "timeout"
    BehaviorFailAfterN BehaviorType = "fail-after-n"
)

type EndpointBehavior struct {
    Action              BehaviorType `json:"action"`
    ResponseCode       int          `json:"responseCode"`
    ResponseBody       string       `json:"responseBody"`
    Delay               int          `json:"delay"`               // milliseconds
    FailCount           int          `json:"count"`               // failure count
    FailResponseCode   int          `json:"failResponseCode"`
    FailResponseBody   string       `json:"failResponseBody"`
}
```

**Request Counter**:

```go
type RequestCounter struct {
    mu      sync.Mutex
    count   int
}

func (c *RequestCounter) Increment() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.count++
    return c.count
}

func (c *RequestCounter) Get() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.count
}
```

### 6.3 Client Implementation

**Test Entry**:

```go
func main() {
    testCaseID := os.Getenv("TEST_CASE_ID")
    callbackAddrs := os.Getenv("CALLBACK_ADDRS")
    
    // Execute corresponding test based on TEST_CASE_ID
    switch testCaseID {
    case "E2E-001":
        testFirstEndpointSuccess(callbackAddrs)
    case "E2E-002":
        testRetryThenSuccess(callbackAddrs)
    // ...
    }
}
```

**GatewayClient Reuse**:

```go
func newGatewayClient(callbackAddrs string) gateway.Client {
    addrs := strings.Split(callbackAddrs, ",")
    
    config := gateway.TLSConfig{
        InsecureSkipVerify: true,
    }
    
    return gateway.NewClientWithMultiEndpoints(
        config,
        addrs[0],  // first address as primary
        nil,       // sandboxStorage
        logger,
    )
}
```

## 7. Execution Flow

### 7.1 Single Test Case Execution

```bash
./run-e2e.sh --case E2E-001
```

### 7.2 Full Test Suite Execution

```bash
./run-e2e.sh --all
```

### 7.3 Output Example

```
========================================
E2E Test Suite - mep-e2e
========================================

[E2E-001] First endpoint success on first try ....... PASS
[E2E-002] First endpoint fails then retries ......... PASS
[E2E-003] Failover to second endpoint succeeds ....... PASS
[E2E-004] Both endpoints return errors ............... PASS
[E2E-005] All endpoints timeout ..................... PASS
[E2E-006] 429 retry then success ..................... PASS
[E2E-007] 408 retry then success ..................... PASS
[E2E-008] 400 no retry ............................... PASS
[E2E-009] 401 no retry ............................... PASS
[E2E-010] Client config error causes timeout ........ PASS

========================================
Total: 10 | Passed: 10 | Failed: 0
========================================
```

## 8. Items to Confirm

> **Note**: Container orchestration confirmed - use direct `docker run` (no Docker Compose, Docker Swarm, or Kubernetes). Designed for single VM environment.

1. **Log collection**: ✓ Confirmed - Collect detailed client/server logs for debugging
   - Client logs: Capture GatewayClient request/response details, retry attempts, errors
   - Server logs: Capture incoming requests, behavior decisions, response details
   - Output: Logs stored in `./logs/{test-case-id}/` directory with timestamp

2. **Parallel execution**: ✓ Confirmed - Support multiple test cases running in parallel
   - Max parallel workers: Configurable via `--parallel` flag (default: 4)
   - Each test case runs in isolated container with unique ports
   - Test results aggregated after all parallel runs complete

3. **Report format**: ✓ Confirmed - Generate HTML test reports
   - Report location: `./reports/e2e-report-{timestamp}.html`
   - Report contents: Test summary, pass/fail status, execution time, detailed logs per case
   - Includes visual charts (pie chart for pass rate, bar chart for timing)

4. **CI integration**: Not required for now - will be manually integrated later

---

Please confirm if the above proposal meets your expectations. Once confirmed, I will proceed with implementation.