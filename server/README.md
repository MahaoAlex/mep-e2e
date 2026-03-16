# E2E Mock Server

This is the mock HTTP server for E2E testing. It simulates different endpoint behaviors based on environment variables.

## Build

```bash
docker build -t agw-e2e-server:latest .
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| TEST_CASE_ID | Test case identifier | - |
| SERVER_PORT_1 | Primary server port | 8080 |
| SERVER_PORT_2 | Backup server port | 8081 |
| TIMEOUT | Request timeout (seconds) | 10 |
| BEHAVIOR_ACTION | Action type: success, error, timeout, fail-after-n | success |
| RESPONSE_CODE | HTTP response code for success | 200 |
| RESPONSE_BODY | Response body for success | {"code":0,"msg":"success","request_id":"default"} |
| DELAY | Response delay in milliseconds | 0 |
| FAIL_COUNT | Number of failures before success (for fail-after-n) | 1 |
| FAIL_RESPONSE_CODE | HTTP response code for failure | 500 |
| FAIL_RESPONSE_BODY | Response body for failure | {"code":500,"msg":"error"} |