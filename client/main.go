package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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

func main() {
	testCaseID := os.Getenv("TEST_CASE_ID")
	callbackAddrs := os.Getenv("CALLBACK_ADDRS")
	_ = os.Getenv("EXPECTED_HTTP_CODE")
	expectedResponseCode := os.Getenv("EXPECTED_RESPONSE_CODE")
	expectedMsg := os.Getenv("EXPECTED_MSG")
	expectedError := os.Getenv("EXPECTED_ERROR")

	log.Printf("Starting E2E test: %s", testCaseID)
	log.Printf("Callback addresses: %s", callbackAddrs)

	if testCaseID == "" {
		log.Fatal("TEST_CASE_ID is required")
	}

	if callbackAddrs == "" {
		log.Fatal("CALLBACK_ADDRS is required")
	}

	addrs := strings.Split(callbackAddrs, ",")
	if len(addrs) == 0 {
		log.Fatal("No callback addresses provided")
	}

	clientError := os.Getenv("CLIENT_ERROR") == "true"

	var err error
	var responseCode int
	var responseMsg string

	if clientError {
		_, err = http.Get("http://invalid-host-that-does-not-exist:9999/register")
		responseCode = 0
	} else {
		resp, rErr := makeRequest(addrs[0])
		if rErr != nil {
			err = rErr
			responseCode = 0
		} else {
			responseCode = resp.Code
			responseMsg = resp.Message
		}
	}

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

	if result.Pass {
		log.Printf("TEST PASSED: %s", testCaseID)
		fmt.Println("PASS")
		os.Exit(0)
	}

	log.Printf("TEST FAILED: %s - %s", testCaseID, result.ErrorMsg)
	fmt.Printf("FAIL: %s\n", result.ErrorMsg)
	os.Exit(1)
}

func makeRequest(addr string) (*RegisterResponse, error) {
	url := fmt.Sprintf("http://%s/v3/api/sandbox/register", strings.TrimSpace(addr))

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

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("connection refused: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var result RegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v, body: %s", err, string(respBody))
	}

	return &result, nil
}
