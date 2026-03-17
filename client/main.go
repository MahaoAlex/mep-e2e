package main

import (
	"agw-e2e/client/gateway"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mep-e2e/pkg/logger"
	"net/http"
	"os"
	"strings"
	"time"
)

type RegisterRequest struct {
	SandboxID         string `json:"sandbox_id"`
	HostAddress       string `json:"host_address"`
	CellID            string `json:"cell_id"`
	SandboxTemplateID string `json:"sandbox_template_id"`
}

type RegisterResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"msg"`
	RequestID string `json:"request_id"`
}

type ExpectedConfig struct {
	Error        string
	ResponseCode string
	Message      string
}

type ActualResult struct {
	Error        error
	ResponseCode int
	Message      string
}

type ValidationResult struct {
	Pass     bool
	ErrorMsg string
}

type RequestLog struct {
	Attempt      int
	URL          string
	Method       string
	RequestBody  string
	StatusCode   int
	ResponseBody string
	Error        string
	Duration     time.Duration
}

type MockSandboxStorage struct {
	callbackAddresses []string
}

func NewMockSandboxStorage(callbackAddresses []string) *MockSandboxStorage {
	return &MockSandboxStorage{
		callbackAddresses: callbackAddresses,
	}
}

func (m *MockSandboxStorage) Get(ctx context.Context, key string) (*gateway.Sandbox, error) {
	if key == "" {
		return nil, fmt.Errorf("sandbox ID is empty")
	}
	return &gateway.Sandbox{
		CallbackAddresses: m.callbackAddresses,
	}, nil
}

var requestLogs []RequestLog

func validateResult(expected ExpectedConfig, actual ActualResult) ValidationResult {
	if actual.Error != nil {
		if expected.Error != "" && !strings.Contains(actual.Error.Error(), expected.Error) {
			return ValidationResult{Pass: false, ErrorMsg: fmt.Sprintf("Expected error containing '%s', got: %v", expected.Error, actual.Error)}
		}
		if expected.Error == "" {
			return ValidationResult{Pass: false, ErrorMsg: fmt.Sprintf("Unexpected error: %v", actual.Error)}
		}
		return ValidationResult{Pass: true}
	}

	if expected.Error != "" {
		return ValidationResult{Pass: false, ErrorMsg: fmt.Sprintf("Expected error containing '%s', but got success", expected.Error)}
	}

	if expected.ResponseCode != "" {
		expectedCode := 0
		fmt.Sscanf(expected.ResponseCode, "%d", &expectedCode)
		if actual.ResponseCode != expectedCode {
			return ValidationResult{Pass: false, ErrorMsg: fmt.Sprintf("Expected response code %d, got %d", expectedCode, actual.ResponseCode)}
		}
	}

	if expected.Message != "" && actual.Message != expected.Message {
		return ValidationResult{Pass: false, ErrorMsg: fmt.Sprintf("Expected message '%s', got '%s'", expected.Message, actual.Message)}
	}

	return ValidationResult{Pass: true}
}

func createHTTPClient() (*http.Client, error) {
	enableHTTPS := os.Getenv("ENABLE_HTTPS") == "true"
	caCertFile := os.Getenv("CA_CERT_FILE")

	if !enableHTTPS {
		return &http.Client{Timeout: 5 * time.Second}, nil
	}

	caCert, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		RootCAs: caCertPool,
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}, nil
}

func printRequestLogs() {
	logger.Println("========== REQUEST LOGS ==========")
	for i, reqLog := range requestLogs {
		logger.Printf("---------- Request #%d ----------", i+1)
		logger.Printf("  URL: %s", reqLog.URL)
		logger.Printf("  Method: %s", reqLog.Method)
		logger.Printf("  Request Body: %s", reqLog.RequestBody)
		logger.Printf("  Status Code: %d", reqLog.StatusCode)
		logger.Printf("  Response Body: %s", reqLog.ResponseBody)
		if reqLog.Error != "" {
			logger.Printf("  Error: %s", reqLog.Error)
		}
		logger.Printf("  Duration: %v", reqLog.Duration)
	}
	logger.Println("==================================")
}

func main() {
	testCaseID := os.Getenv("TEST_CASE_ID")
	callbackAddrs := os.Getenv("CALLBACK_ADDRS")
	expectedHTTPCode := os.Getenv("EXPECTED_HTTP_CODE")
	expectedResponseCode := os.Getenv("EXPECTED_RESPONSE_CODE")
	expectedMsg := os.Getenv("EXPECTED_MSG")
	expectedError := os.Getenv("EXPECTED_ERROR")
	enableHTTPS := os.Getenv("ENABLE_HTTPS") == "true"
	caCert := os.Getenv("CA_CERT_FILE")
	clientCert := os.Getenv("CLIENT_CERT_FILE")
	clientKey := os.Getenv("CLIENT_KEY_FILE")
	agentGatewayDomain := os.Getenv("AGENT_GATEWAY_DOMAIN")

	logFile := os.Getenv("LOG_FILE")
	logDir := os.Getenv("LOG_DIR")
	enableFileLog := os.Getenv("ENABLE_FILE_LOG") == "true"

	if agentGatewayDomain == "" {
		agentGatewayDomain = "agent-gateway.e2e.region.com"
	}

	logCfg := logger.Config{
		EnableConsole: true,
		EnableFile:    enableFileLog,
		LogFile:       logFile,
		LogDir:        logDir,
		LogFileName:   fmt.Sprintf("client-%s.log", testCaseID),
	}
	if err := logger.Init(logCfg); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Close()

	logger.Println("============================================")
	logger.Printf("TEST CASE: %s", testCaseID)
	logger.Println("============================================")
	logger.Printf("Test Scenario: %s", getTestScenario(testCaseID))
	logger.Println("")
	logger.Println("=== Expected Output ===")
	if expectedHTTPCode != "" {
		logger.Printf("  HTTP Code: %s", expectedHTTPCode)
	}
	if expectedResponseCode != "" {
		logger.Printf("  Response Code: %s", expectedResponseCode)
	}
	if expectedMsg != "" {
		logger.Printf("  Response Message: %s", expectedMsg)
	}
	if expectedError != "" {
		logger.Printf("  Error Contains: %s", expectedError)
	}
	logger.Println("")
	logger.Printf("Callback addresses: %s", callbackAddrs)
	logger.Printf("HTTPS enabled: %v", enableHTTPS)
	logger.Println("")

	if testCaseID == "" {
		logger.Fatal("TEST_CASE_ID is required")
	}

	if callbackAddrs == "" {
		logger.Fatal("CALLBACK_ADDRS is required")
	}

	addrs := strings.Split(callbackAddrs, ",")
	if len(addrs) == 0 {
		logger.Fatal("No callback addresses provided")
	}

	var err error
	var responseCode int
	var responseMsg string

	sandboxStorage := NewMockSandboxStorage(addrs)

	var gatewayClient gateway.Client
	if agentGatewayDomain != "" || caCert != "" || clientCert != "" {
		gatewayClient = gateway.NewClientWithMultiEndpoints(
			gateway.TLSConfig{
				CACertPath:         caCert,
				ClientCertPath:     clientCert,
				ClientKeyPath:      clientKey,
				InsecureSkipVerify: true,
			},
			agentGatewayDomain,
			sandboxStorage,
			logger.Default().Logger,
		)
		logger.Printf("Gateway client initialized for domain: %s", agentGatewayDomain)
	} else {
		gatewayClient = gateway.NewClientWithMultiEndpoints(
			gateway.TLSConfig{
				InsecureSkipVerify: true,
			},
			"",
			sandboxStorage,
			logger.Default().Logger,
		)
		logger.Printf("Gateway client initialized without TLS")
	}

	ctx := context.Background()
	req := &gateway.RegisterSandboxRequest{
		SandboxID:         "test-sandbox-001",
		HostAddress:       "192.168.1.100",
		CellID:            "test-cell-001",
		SandboxTemplateID: "template-001",
	}

	resp, rErr := gatewayClient.RegisterSandbox(ctx, req)
	if rErr != nil {
		err = rErr
		responseCode = 0
	} else {
		responseCode = resp.Code
		responseMsg = resp.Message
	}

	logger.Println("")
	logger.Println("=== Actual Output ===")
	if err != nil {
		logger.Printf("  Error: %v", err)
	} else {
		logger.Printf("  Response Code: %d", responseCode)
		logger.Printf("  Response Message: %s", responseMsg)
	}
	logger.Println("")

	printRequestLogs()

	result := validateResult(
		ExpectedConfig{
			Error:        expectedError,
			ResponseCode: expectedResponseCode,
			Message:      expectedMsg,
		},
		ActualResult{
			Error:        err,
			ResponseCode: responseCode,
			Message:      responseMsg,
		},
	)

	logger.Println("=== Comparison ===")

	if expectedError != "" {
		if err != nil && strings.Contains(err.Error(), expectedError) {
			logger.Printf("  Error Match:     PASS (expected contains '%s', got '%v')", expectedError, err)
		} else if err != nil {
			logger.Printf("  Error Match:     FAIL (expected contains '%s', got '%v')", expectedError, err)
		} else {
			logger.Printf("  Error Match:     FAIL (expected error containing '%s', but got success)", expectedError)
		}
	} else if err != nil {
		logger.Printf("  Error Match:     FAIL (unexpected error: %v)", err)
	}

	if expectedResponseCode != "" {
		expectedCode := 0
		fmt.Sscanf(expectedResponseCode, "%d", &expectedCode)
		if responseCode == expectedCode {
			logger.Printf("  Response Code:   PASS (expected %d, got %d)", expectedCode, responseCode)
		} else {
			logger.Printf("  Response Code:   FAIL (expected %d, got %d)", expectedCode, responseCode)
		}
	}

	if expectedMsg != "" {
		if responseMsg == expectedMsg {
			logger.Printf("  Response Msg:    PASS (expected '%s', got '%s')", expectedMsg, responseMsg)
		} else {
			logger.Printf("  Response Msg:    FAIL (expected '%s', got '%s')", expectedMsg, responseMsg)
		}
	}

	logger.Println("")

	logger.Println("============================================")
	if result.Pass {
		logger.Printf("RESULT: PASSED")
		fmt.Println("PASS")
		os.Exit(0)
	}

	logger.Printf("RESULT: FAILED")
	logger.Printf("  Reason: %s", result.ErrorMsg)
	fmt.Printf("FAIL: %s\n", result.ErrorMsg)
	os.Exit(1)
}

func getTestScenario(testCaseID string) string {
	scenarios := map[string]string{
		"E2E-001": "First endpoint succeeds on first try",
		"E2E-002": "First endpoint fails then retries successfully",
		"E2E-003": "Failover to second endpoint succeeds",
		"E2E-004": "Both endpoints return errors",
		"E2E-005": "All endpoints timeout",
		"E2E-006": "First endpoint returns 429 then retries successfully",
		"E2E-007": "First endpoint returns 408 then retries successfully",
		"E2E-008": "First endpoint returns 400 no retry",
		"E2E-009": "First endpoint returns 401 no retry",
		"E2E-010": "Client config error causes timeout no retry",
	}
	if s, ok := scenarios[testCaseID]; ok {
		return s
	}
	return "Unknown test scenario"
}

func makeRequest(addr string, attempt int) (*RegisterResponse, error) {
	enableHTTPS := os.Getenv("ENABLE_HTTPS") == "true"

	scheme := "http"
	if enableHTTPS {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s/v3/api/sandbox/register", scheme, strings.TrimSpace(addr))

	reqBody := RegisterRequest{
		SandboxID:         "test-sandbox-001",
		HostAddress:       "192.168.1.100",
		CellID:            "test-cell-001",
		SandboxTemplateID: "template-001",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	client, err := createHTTPClient()
	if err != nil {
		return nil, err
	}

	logger.Printf(">>> Request #%d: POST %s", attempt, url)
	logger.Printf(">>> Request Body: %s", string(body))

	start := time.Now()
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	duration := time.Since(start)

	reqLog := RequestLog{
		Attempt:     attempt,
		URL:         url,
		Method:      "POST",
		RequestBody: string(body),
		Duration:    duration,
	}

	if err != nil {
		reqLog.Error = err.Error()
		requestLogs = append(requestLogs, reqLog)
		logger.Printf("<<< Response #%d: ERROR - %v (duration: %v)", attempt, err, duration)
		return nil, fmt.Errorf("connection refused: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		reqLog.Error = err.Error()
		requestLogs = append(requestLogs, reqLog)
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	reqLog.StatusCode = resp.StatusCode
	reqLog.ResponseBody = string(respBody)
	requestLogs = append(requestLogs, reqLog)

	logger.Printf("<<< Response #%d: HTTP %d (duration: %v)", attempt, resp.StatusCode, duration)
	logger.Printf("<<< Response Body: %s", string(respBody))

	var result RegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v, body: %s", err, string(respBody))
	}

	return &result, nil
}
