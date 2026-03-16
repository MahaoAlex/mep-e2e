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

| Component       | Responsibilities                                                                    |
| --------------- | ----------------------------------------------------------------------------------- |
| `run-e2e.sh`    | Parse strategy file, coordinate container lifecycle, execute tests, collect results |
| `strategy.yaml` | Define test cases, server behaviors, endpoint configurations                        |
| `client/`       | Test client integrating business code, reusing existing AgentGatewayClient          |
| `server/`       | Multi-endpoint mock service, returning HTTP responses as configured                 |

## 3. Test Scenario Design

### 3.1 Scenario Matrix

| Scenario ID | Scenario Description                                    | Expected Result                      |
| ----------- | ------------------------------------------------------- | ------------------------------------ |
| E2E-001     | First endpoint succeeds on first request                | Returns 200 + correct response body  |
| E2E-002     | First request fails, retry succeeds                     | Returns 200 + correct response body  |
| E2E-003     | First endpoint fails twice, failover to second succeeds | Returns 200 + correct response body  |
| E2E-004     | First returns 500, second returns 404                   | Returns last received error response |
| E2E-005     | All endpoints timeout                                   | Returns timeout error                |
| E2E-006     | First returns 429, retry succeeds                       | Returns 200 + correct response body  |
| E2E-007     | First returns 408, retry succeeds                       | Returns 200 + correct response body  |
| E2E-008     | First returns 400, no retry                             | Returns 400 error                    |
| E2E-009     | First returns 401, no retry                             | Returns 401 error                    |
| E2E-010     | Client config error causes timeout, no retry            | Returns timeout error                |

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

## 3.4 Error Definition

According to README.md, the multi-endpoint error handling is defined as follows:

### AttemptError Structure

Each HTTP request attempt records an `AttemptError`:

```go
type AttemptError struct {
    AttemptNumber    int       `json:"attempt_number"`    // Attempt number, starting from 1
    RequestID        string    `json:"request_id"`       // Request-ID for distributed tracing
    Endpoint         string    `json:"endpoint"`         // Request endpoint address
    UnifiedErrorCode int       `json:"unified_error_code"` // Unified error code
    UnifiedErrorDetail string  `json:"unified_error_detail"` // Error details
    Timestamp        string    `json:"timestamp"`        // RFC3339 format
    ErrorType        ErrorType `json:"error_type"`       // Error type
}
```

### Error Type Constants

```go
// Client error types
ErrorTypeClientConfig  ErrorType = "client_config_error"  // Client configuration error
ErrorTypeClientNetwork ErrorType = "client_network_error" // Client network error
ErrorTypeClientRequest ErrorType = "client_request_error" // Client request error
ErrorTypeClientTimeout ErrorType = "client_timeout_error" // Client timeout error

// Server error types
ErrorTypeServer4xx     ErrorType = "server_error_4xx"     // Server 4xx business error
ErrorTypeServer5xx     ErrorType = "server_error_5xx"     // Server 5xx system error
ErrorTypeServerUnknown ErrorType = "server_error_unknown"

// Unhandled system error or non-HTTP error
ErrorTypeUnexpectedError ErrorType = "unexpected_error"

// Unified error codes
UnifiedCodeClientDefault   = 0  // Fixed error code for all client-side errors
UnifiedUnexpectErrorCode   = 1  // Unexpected error code
```

### Error Code Mapping

| Error Type                 | UnifiedErrorCode                  | UnifiedErrorDetail                    |
| -------------------------- | --------------------------------- | ------------------------------------- |
| `ErrorTypeClientConfig`    | 0                                 | Specific network errors, timeout info |
| `ErrorTypeClientNetwork`   | 0                                 | Network connection errors             |
| `ErrorTypeClientTimeout`   | 0                                 | "request timed out"                   |
| `ErrorTypeServer4xx`       | HTTP status (e.g., 400, 401, 404) | Server response body                  |
| `ErrorTypeServer5xx`       | HTTP status (e.g., 500, 503)      | Server response body                  |
| `ErrorTypeUnexpectedError` | 1                                 | Unexpected error message              |

### MultiEndpointError Error Message Format

```go
func (e *MultiEndpointError) Error() string {
    last := e.AttemptErrors[len(e.AttemptErrors)-1]

    switch last.ErrorType {
    case ErrorTypeClientConfig:
        msg = "client configuration error: " + last.UnifiedErrorDetail
    case ErrorTypeClientNetwork:
        msg = "client network error: " + last.UnifiedErrorDetail
    case ErrorTypeClientTimeout:
        msg = "client timeout error: request timed out"
    case ErrorTypeServer4xx:
        msg = fmt.Sprintf("server 4xx error: [%d] %s", last.UnifiedErrorCode, last.UnifiedErrorDetail)
    case ErrorTypeServer5xx:
        msg = fmt.Sprintf("server 5xx error: [%d] %s", last.UnifiedErrorCode, last.UnifiedErrorDetail)
    default:
        msg = fmt.Sprintf("unhandled error type [%s]: %s", last.ErrorType, last.UnifiedErrorDetail)
    }

    return fmt.Sprintf("%s (total attempts: %d, group: %s)", msg, e.TotalAttempts, e.GroupName)
}
```

### Expected Error Validation

For test cases expecting errors, use the `errorContains` field in `expected`:

```yaml
# E2E-005: Timeout scenario
expected:
  httpCode: 0
  errorContains: "timeout"

# E2E-008: 400 Bad Request
expected:
  httpCode: 400
  errorContains: "server 4xx error"

# E2E-010: Client config error
expected:
  httpCode: 0
  errorContains: "client configuration error"
```

## 4. Strategy File Design

> **Note**: RetryPolicy uses global configuration. All test cases share the same policy defined in the business code. This validates the existing business logic without varying policy parameters.

### 4.1 strategy.yaml Structure

```yaml
version: "1.0"

# Global config
config:
  timeout: 60s # Maximum execution time for a single test case (includes container startup, request/response, etc.)
  logLevel: debug # Logging level: debug, info, warn, error

# Global RetryPolicy (applied to all test cases)
# Based on business code: getRegisterEndpointGroup
retryPolicy:
  # Primary endpoint (index 0)
  primary:
    maxAttempts: 2 # 2 attempts (1 initial + 1 retry)
    timeout: 5s # HTTP request timeout for primary endpoint (5 seconds per request)
    interval: 0 # no backoff interval

  # Backup endpoints (index > 0)
  backup:
    maxAttempts: 1 # 1 attempt (no retry)
    timeout: 5s # HTTP request timeout for backup endpoints
    interval: 0

# Test case list
testCases:
  - id: "E2E-001"
    name: "First endpoint success on first try"
    description: "Verify first endpoint succeeds on first request"
    enabled: true

    # Expected result (for test validation)
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"

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

### 4.2 Expected Result Validation

Each test case defines `expected` results to validate:

```yaml
expected:
  httpCode: 200 # Expected HTTP status code
  responseCode: 0 # Expected business response code
  responseMsg: "success" # Expected response message
  errorContains: "" # Optional: expected error substring (for error cases)
```

**Success Case Example (E2E-001)**:

```yaml
expected:
  httpCode: 200
  responseCode: 0
  responseMsg: "success"
```

**Error Case Example (E2E-004 - Both endpoints return errors)**:

```yaml
expected:
  httpCode: 404 # Last endpoint returns 404
  responseCode: 404
  responseMsg: "not found"
```

**Timeout Case Example (E2E-005)**:

```yaml
expected:
  httpCode: 0 # 0 indicates timeout/connection error
  errorContains: "timeout" # Match error message contains "timeout"
```

### 4.3 Behavior Config

Server supports the following behavior types:

| Type           | Description                         | Parameters                              |
| -------------- | ----------------------------------- | --------------------------------------- |
| `success`      | Return success response             | `responseCode`, `responseBody`, `delay` |
| `error`        | Return error response               | `responseCode`, `responseBody`          |
| `timeout`      | Simulate timeout                    | `delay` (exceeds client timeout)        |
| `fail-after-n` | First N requests fail, then succeed | `count`, `failResponseCode`             |

### 4.4 Test Case Configuration Examples

```yaml
version: "1.0"

config:
  timeout: 60s # Maximum execution time for a single test case
  logLevel: debug

retryPolicy:
  primary:
    maxAttempts: 2
    timeout: 5s
  backup:
    maxAttempts: 1
    timeout: 5s

testCases:
  # E2E-001: First endpoint succeeds on first try
  - id: "E2E-001"
    name: "First endpoint success on first try"
    enabled: true
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"
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
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"
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
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"
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
    expected:
      httpCode: 404
      responseCode: 404
      responseMsg: "not found"
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
    expected:
      httpCode: 0
      errorContains: "timeout"
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
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"
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
    expected:
      httpCode: 200
      responseCode: 0
      responseMsg: "success"
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
    expected:
      httpCode: 400
      responseCode: 400
      responseMsg: "bad request"
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
    expected:
      httpCode: 401
      responseCode: 401
      responseMsg: "unauthorized"
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
    expected:
      httpCode: 0
      errorContains: "connection refused"
    client:
      image: "e2e-client:latest"
      env:
        - TEST_CASE_ID=E2E-010
        - CALLBACK_ADDRS=invalid-host:9999
        - CLIENT_ERROR=true
    server:
      endpoints: []
```

### 4.5 Test Case Summary Table

> **Note**: All test cases use the global RetryPolicy (primary: 2 attempts, 5s timeout; backup: 1 attempt, 5s timeout).

| ID      | Test Name                                      | Expected Result                                       | Server Endpoint Behavior                                                                       | Description                                      |
| ------- | ---------------------------------------------- | ----------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| E2E-001 | First endpoint success on first try            | httpCode: 200, responseCode: 0, msg: "success"        | `action: success`                                                                              | Verify first endpoint succeeds on first request  |
| E2E-002 | First endpoint fails then retries successfully | httpCode: 200, responseCode: 0, msg: "success"        | `action: fail-after-n, count: 1`                                                               | First request returns 503, retry succeeds        |
| E2E-003 | Failover to second endpoint succeeds           | httpCode: 200, responseCode: 0, msg: "success"        | Endpoint1: `fail-after-n, count: 2`<br>Endpoint2: `action: success`                            | Primary fails twice, failover to backup succeeds |
| E2E-004 | Both endpoints return errors                   | httpCode: 404, responseCode: 404, msg: "not found"    | Endpoint1: `action: error, responseCode: 500`<br>Endpoint2: `action: error, responseCode: 404` | First returns 500, second returns 404            |
| E2E-005 | All endpoints timeout                          | httpCode: 0, errorContains: "timeout"                 | Endpoint1: `action: timeout, delay: 10000`<br>Endpoint2: `action: timeout, delay: 10000`       | All endpoints timeout, no response               |
| E2E-006 | 429 retry then success                         | httpCode: 200, responseCode: 0, msg: "success"        | `action: fail-after-n, count: 1, failResponseCode: 429`                                        | First returns 429 (retryable), retry succeeds    |
| E2E-007 | 408 retry then success                         | httpCode: 200, responseCode: 0, msg: "success"        | `action: fail-after-n, count: 1, failResponseCode: 408`                                        | First returns 408 (retryable), retry succeeds    |
| E2E-008 | 400 no retry                                   | httpCode: 400, responseCode: 400, msg: "bad request"  | `action: error, responseCode: 400`                                                             | 4xx error not retried on same endpoint           |
| E2E-009 | 401 no retry                                   | httpCode: 401, responseCode: 401, msg: "unauthorized" | `action: error, responseCode: 401`                                                             | 4xx error not retried on same endpoint           |
| E2E-010 | Client config error causes timeout             | httpCode: 0, errorContains: "connection refused"      | No server endpoints                                                                            | Invalid host causes connection error, no retry   |

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

4. **RetryPolicy**: ✓ Confirmed - Global configuration (Option A)
   - Primary endpoint: 2 attempts (1 initial + 1 retry), 5s timeout
   - Backup endpoints: 1 attempt, 5s timeout
   - All test cases share the same policy from business code

5. **CI integration**: Not required for now - will be manually integrated later

---

Please confirm if the above proposal meets your expectations. Once confirmed, I will proceed with implementation.

