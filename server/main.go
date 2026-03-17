package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mep-e2e/pkg/logger"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	Port1   int
	Port2   int
	Timeout int
}

type ActionType string

const (
	ActionSuccess    ActionType = "success"
	ActionError      ActionType = "error"
	ActionTimeout    ActionType = "timeout"
	ActionFailAfterN ActionType = "fail-after-n"
)

type EndpointBehavior struct {
	Action              ActionType
	ResponseCode        int
	ResponseBody        string
	Delay               int
	FailCount           int
	FailResponseCode    int
	FailResponseBody    string
	SuccessResponseCode int
	SuccessResponseBody string
	requestCounter      int32
}

func (b *EndpointBehavior) GenerateResponse() Response {
	b.requestCounter++
	count := b.requestCounter

	switch b.Action {
	case ActionSuccess, ActionError:
		return Response{
			StatusCode: b.ResponseCode,
			Body:       b.ResponseBody,
		}
	case ActionTimeout:
		return Response{
			StatusCode: 408,
			Body:       `{"code":408,"msg":"request timeout","request_id":"timeout"}`,
		}
	case ActionFailAfterN:
		if count <= int32(b.FailCount) {
			return Response{
				StatusCode: b.FailResponseCode,
				Body:       b.FailResponseBody,
			}
		}
		return Response{
			StatusCode: b.SuccessResponseCode,
			Body:       b.SuccessResponseBody,
		}
	default:
		return Response{
			StatusCode: b.ResponseCode,
			Body:       b.ResponseBody,
		}
	}
}

type Response struct {
	StatusCode int
	Body       string
}

func main() {
	cfg := loadConfig()
	_ = cfg

	testCaseID := os.Getenv("TEST_CASE_ID")
	logFile := os.Getenv("LOG_FILE")
	logDir := os.Getenv("LOG_DIR")
	enableFileLog := os.Getenv("ENABLE_FILE_LOG") == "true"

	logCfg := logger.Config{
		EnableConsole: true,
		EnableFile:    enableFileLog,
		LogFile:       logFile,
		LogDir:        logDir,
		LogFileName:   fmt.Sprintf("server-%s.log", testCaseID),
	}
	if err := logger.Init(logCfg); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Close()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	server1Port := 8080
	server2Port := 8081

	if p := os.Getenv("SERVER_PORT_1"); p != "" {
		fmt.Sscanf(p, "%d", &server1Port)
	}
	if p := os.Getenv("SERVER_PORT_2"); p != "" {
		fmt.Sscanf(p, "%d", &server2Port)
	}

	enableHTTPS := os.Getenv("ENABLE_HTTPS") == "true"
	certFile := os.Getenv("CERT_FILE")
	keyFile := os.Getenv("KEY_FILE")

	logger.Printf("E2E mock server starting for test case: %s", testCaseID)
	logger.Printf("HTTPS enabled: %v", enableHTTPS)

	serverBehavior := loadServerBehavior()

	mux := http.NewServeMux()
	mux.HandleFunc("/v3/api/sandbox/register", registerHandler(serverBehavior))
	mux.HandleFunc("/v3/api/sandbox/unregister", unregisterHandler(serverBehavior))
	mux.HandleFunc("/health", healthHandler)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", server1Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Printf("Server 1 listening on port %d (HTTPS: %v)", server1Port, enableHTTPS)
		var err error
		if enableHTTPS && certFile != "" && keyFile != "" {
			err = server.ListenAndServeTLS(certFile, keyFile)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Printf("Server 1 error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Printf("Server 2 listening on port %d (HTTPS: %v)", server2Port, enableHTTPS)
		mux2 := http.NewServeMux()
		mux2.HandleFunc("/v3/api/sandbox/register", registerHandler(serverBehavior))
		mux2.HandleFunc("/v3/api/sandbox/unregister", unregisterHandler(serverBehavior))
		mux2.HandleFunc("/health", healthHandler)

		server2 := &http.Server{
			Addr:         fmt.Sprintf(":%d", server2Port),
			Handler:      mux2,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		var err error
		if enableHTTPS && certFile != "" && keyFile != "" {
			err = server2.ListenAndServeTLS(certFile, keyFile)
		} else {
			err = server2.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Printf("Server 2 error: %v", err)
		}
	}()

	logger.Printf("E2E mock server started, ports: %d, %d", server1Port, server2Port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Println("Shutting down servers...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	server.Shutdown(shutdownCtx)
	wg.Wait()

	logger.Println("Server stopped")
}

func loadConfig() Config {
	return Config{Port1: 8080, Port2: 8081, Timeout: 10}
}

func loadServerBehavior() *EndpointBehavior {
	action := os.Getenv("BEHAVIOR_ACTION")
	if action == "" {
		action = "success"
	}

	respCode := 200
	respBody := `{"code":0,"msg":"success","request_id":"default"}`

	if rc := os.Getenv("RESPONSE_CODE"); rc != "" {
		fmt.Sscanf(rc, "%d", &respCode)
	}
	if rb := os.Getenv("RESPONSE_BODY"); rb != "" {
		respBody = rb
	}

	delay := 0
	if d := os.Getenv("DELAY"); d != "" {
		fmt.Sscanf(d, "%d", &delay)
	}

	failCount := 1
	if fc := os.Getenv("FAIL_COUNT"); fc != "" {
		fmt.Sscanf(fc, "%d", &failCount)
	}

	failRespCode := 500
	failRespBody := `{"code":500,"msg":"error"}`
	if frc := os.Getenv("FAIL_RESPONSE_CODE"); frc != "" {
		fmt.Sscanf(frc, "%d", &failRespCode)
	}
	if frb := os.Getenv("FAIL_RESPONSE_BODY"); frb != "" {
		failRespBody = frb
	}

	successRespCode := 200
	successRespBody := `{"code":0,"msg":"success","request_id":"default"}`
	if src := os.Getenv("SUCCESS_RESPONSE_CODE"); src != "" {
		fmt.Sscanf(src, "%d", &successRespCode)
	}
	if srb := os.Getenv("SUCCESS_RESPONSE_BODY"); srb != "" {
		successRespBody = srb
	}

	return &EndpointBehavior{
		Action:              ActionType(action),
		ResponseCode:        respCode,
		ResponseBody:        respBody,
		Delay:               delay,
		FailCount:           failCount,
		FailResponseCode:    failRespCode,
		FailResponseBody:    failRespBody,
		SuccessResponseCode: successRespCode,
		SuccessResponseBody: successRespBody,
	}
}

func registerHandler(b *EndpointBehavior) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())

		logger.Println("============================================")
		logger.Printf("[REQUEST] %s - Register", requestID)
		logger.Println("============================================")
		logger.Printf("  Source: %s", r.RemoteAddr)
		logger.Printf("  Method: %s %s", r.Method, r.URL.Path)
		logger.Printf("  Headers: %v", r.Header)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Printf("  Error reading body: %v", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			logger.Printf("  Error parsing body: %v", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		logger.Printf("  Request Body: %s", string(body))

		time.Sleep(time.Duration(b.Delay) * time.Millisecond)

		resp := b.GenerateResponse()
		b.requestCounter++

		logger.Println("  ------------------------------------------")
		logger.Printf("  Response Status: %d", resp.StatusCode)
		logger.Printf("  Response Body: %s", resp.Body)
		logger.Printf("  Request Count: %d", b.requestCounter)
		logger.Printf("  Behavior Action: %s", b.Action)
		logger.Println("============================================")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write([]byte(resp.Body))
	}
}

func unregisterHandler(b *EndpointBehavior) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())

		logger.Println("============================================")
		logger.Printf("[REQUEST] %s - Unregister", requestID)
		logger.Println("============================================")
		logger.Printf("  Source: %s", r.RemoteAddr)
		logger.Printf("  Method: %s %s", r.Method, r.URL.Path)
		logger.Printf("  Headers: %v", r.Header)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Printf("  Error reading body: %v", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			logger.Printf("  Error parsing body: %v", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		logger.Printf("  Request Body: %s", string(body))

		time.Sleep(time.Duration(b.Delay) * time.Millisecond)

		resp := b.GenerateResponse()
		b.requestCounter++

		logger.Println("  ------------------------------------------")
		logger.Printf("  Response Status: %d", resp.StatusCode)
		logger.Printf("  Response Body: %s", resp.Body)
		logger.Printf("  Request Count: %d", b.requestCounter)
		logger.Printf("  Behavior Action: %s", b.Action)
		logger.Println("============================================")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write([]byte(resp.Body))
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
