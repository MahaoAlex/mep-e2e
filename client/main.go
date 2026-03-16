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

	// Determine if this is a client error test (invalid host)
	clientError := os.Getenv("CLIENT_ERROR") == "true"

	var err error
	var responseCode int
	var responseMsg string

	if clientError {
		// Simulate client error scenario - invalid endpoint
		_, err = http.Get("http://invalid-host-that-does-not-exist:9999/register")
		responseCode = 0
	} else {
		// Make request to the first endpoint
		resp, rErr := makeRequest(addrs[0])
		if rErr != nil {
			err = rErr
			responseCode = 0
		} else {
			responseCode = resp.Code
			responseMsg = resp.Message
		}
	}

	// Validate results
	pass := true
	var errorMsg string

	if err != nil {
		// Check if expected error contains in actual error
		if expectedError != "" && !strings.Contains(err.Error(), expectedError) {
			pass = false
			errorMsg = fmt.Sprintf("Expected error containing '%s', got: %v", expectedError, err)
		} else if expectedError == "" {
			pass = false
			errorMsg = fmt.Sprintf("Unexpected error: %v", err)
		}
	} else if expectedError != "" {
		// Expected an error but got success
		pass = false
		errorMsg = fmt.Sprintf("Expected error containing '%s', but got success", expectedError)
	} else {
		// Validate response codes
		if expectedResponseCode != "" {
			expectedCode := 0
			fmt.Sscanf(expectedResponseCode, "%d", &expectedCode)
			if responseCode != expectedCode {
				pass = false
				errorMsg = fmt.Sprintf("Expected response code %d, got %d", expectedCode, responseCode)
			}
		}

		if expectedMsg != "" && responseMsg != expectedMsg {
			pass = false
			errorMsg = fmt.Sprintf("Expected message '%s', got '%s'", expectedMsg, responseMsg)
		}
	}

	if pass {
		log.Printf("TEST PASSED: %s", testCaseID)
		fmt.Println("PASS")
		os.Exit(0)
	} else {
		log.Printf("TEST FAILED: %s - %s", testCaseID, errorMsg)
		fmt.Printf("FAIL: %s\n", errorMsg)
		os.Exit(1)
	}
}

func makeRequest(addr string) (*RegisterResponse, error) {
	url := fmt.Sprintf("http://%s/register", strings.TrimSpace(addr))

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
