package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type SandboxStorage interface {
	Get(ctx context.Context, key string) (*Sandbox, error)
}

type Sandbox struct {
	CallbackAddresses []string
}

type TLSConfig struct {
	CACertPath         string
	ClientCertPath     string
	ClientKeyPath      string
	InsecureSkipVerify bool
}

type RegisterSandboxRequest struct {
	SandboxID         string
	HostAddress       string
	CellID            string
	SandboxTemplateID string
}

type RegisterSandboxResponse struct {
	Code      int
	Message   string
	RequestID string
}

type Client interface {
	RegisterSandbox(ctx context.Context, req *RegisterSandboxRequest) (*RegisterSandboxResponse, error)
}

type ClientImpl struct {
	tlsConfig      TLSConfig
	domain         string
	storage        SandboxStorage
	logger         *log.Logger
	httpClient     *http.Client
	requestCounter int32
	mu             sync.Mutex
}

func NewClientWithMultiEndpoints(tlsConfig TLSConfig, domain string, storage SandboxStorage, logger *log.Logger) Client {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	return &ClientImpl{
		tlsConfig:  tlsConfig,
		domain:     domain,
		storage:    storage,
		logger:     logger,
		httpClient: httpClient,
	}
}

func (c *ClientImpl) RegisterSandbox(ctx context.Context, req *RegisterSandboxRequest) (*RegisterSandboxResponse, error) {
	sandbox, err := c.storage.Get(ctx, req.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %v", err)
	}

	if len(sandbox.CallbackAddresses) == 0 {
		return nil, fmt.Errorf("no callback addresses available")
	}

	var lastErr error
	for _, addr := range sandbox.CallbackAddresses {
		c.mu.Lock()
		c.requestCounter++
		attempt := c.requestCounter
		c.mu.Unlock()

		c.logger.Printf(">>> Attempt #%d: Registering sandbox to %s", attempt, addr)
		c.logger.Printf(">>> Request: SandboxID=%s, HostAddress=%s, CellID=%s",
			req.SandboxID, req.HostAddress, req.CellID)

		resp, err := c.makeRequest(ctx, addr, req)
		if err != nil {
			lastErr = err
			c.logger.Printf("<<< Attempt #%d: FAILED - %v", attempt, err)
			continue
		}

		c.logger.Printf("<<< Attempt #%d: SUCCESS - Code=%d, Message=%s", attempt, resp.Code, resp.Message)
		return resp, nil
	}

	return nil, fmt.Errorf("all endpoints failed, last error: %v", lastErr)
}

func (c *ClientImpl) makeRequest(ctx context.Context, addr string, req *RegisterSandboxRequest) (*RegisterSandboxResponse, error) {
	url := fmt.Sprintf("http://%s/v3/api/sandbox/register", strings.TrimSpace(addr))

	start := time.Now()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(c.buildRequestBody(req)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("request failed after %v: %v", duration, err)
	}
	defer resp.Body.Close()

	c.logger.Printf("<<< Response from %s: HTTP %d (duration: %v)", addr, resp.StatusCode, duration)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return &RegisterSandboxResponse{
		Code:      0,
		Message:   "success",
		RequestID: fmt.Sprintf("req-%d", time.Now().UnixNano()),
	}, nil
}

func (c *ClientImpl) buildRequestBody(req *RegisterSandboxRequest) string {
	return fmt.Sprintf(`{"sandbox_id":"%s","host_address":"%s","cell_id":"%s","sandbox_template_id":"%s"}`,
		req.SandboxID, req.HostAddress, req.CellID, req.SandboxTemplateID)
}
