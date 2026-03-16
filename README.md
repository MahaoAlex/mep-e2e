# mep-e2e

## 背景（当前业务实现参考）

### 申请GatewayClient

```go
// Initialize gateway client if configured
	var gatewayClient gateway.Client
	// Use multi-endpoint client if TLS configuration is provided
	if config.AgentGatewayDomain != "" || config.CACert != "" || config.ClientCert != "" {
		gatewayClient = gateway.NewClientWithMultiEndpoints(
			gateway.TLSConfig{
				CACertPath:     config.CACert,
				ClientCertPath: config.ClientCert,
				ClientKeyPath:  config.ClientKey,
				//InsecureSkipVerify: config.SkipTLSVerify,
				// // TODO: Hardcode to true for testing; revert before production.
				InsecureSkipVerify: true,
			},
			config.AgentGatewayDomain,
			sandboxStorage,
			logger,
		)
	}

```

```go
func NewClientWithMultiEndpoints(
	tlsConfig TLSConfig,
	agentGatewayDomain string,
	sandboxStorage SandboxStorage,
	logger logr.Logger,
) *AgentGatewayClient {

}

type AgentGatewayClient struct {
	// Use the interface from retryablehttp tool library
	multiEndpointClient interface {
		DoWithMultiEndpoints(ctx context.Context, group retryablehttp.EndpointGroup, req *http.Request) (*http.Response, error)
	}
	agentGatewayDomain string
	sandboxStorage     SandboxStorage
	logger             logr.Logger
}
```

```go
func NewDefaultMultiEndpointClient(configures ...MultiEndpointConfigure) *DefaultMultiEndpointClient {
	// Initialize the internal core client with default pool settings
	poolConfig := DefaultConnectionPoolConfig()

	// Build the base HTTP client
	baseClient := NewClient(
		createHTTPClient(poolConfig),
		WithBackoffPolicy(NoRetryBackoff),
	)

	// Construct the core MultiEndpointClient
	coreClient := &MultiEndpointClient{
		baseClient:    baseClient,
		poolConfig:    poolConfig,
		enableMetrics: false,
	}

	// Apply functional options (Dependency Injection/Configuration)
	for _, configure := range configures {
		configure(coreClient)
	}

	//  Wrap the core client in the Default implementation
	return &DefaultMultiEndpointClient{
		client: coreClient,
	}
}
```

### 基于申请的GatewayClient，进行多端点请求

```go
func (c *AgentGatewayClient) RegisterSandbox(ctx context.Context, regParams *RegisterSandboxRequest) (*RegisterSandboxResponse, error) {
	if regParams == nil {
		c.logger.Error(nil, "RegisterSandbox request cannot be nil")
		return nil, fmt.Errorf("register request cannot be nil")
	}

	c.logger.Info("Registering sandbox to AgentGateway",
		"sandboxID", regParams.SandboxID,
		"hostAddress", regParams.HostAddress,
		"cellID", regParams.CellID,
		"sandboxTemplateID", regParams.SandboxTemplateID)

	resp, err := executeGatewayRequest[*RegisterSandboxResponse](
		ctx, c, regParams.SandboxID, string(registerSandboxURL), regParams, c.getRegisterEndpointGroup,
	)
	if err != nil {
		c.logger.Error(err, "Failed to register sandbox", "sandboxID", regParams.SandboxID)
		return nil, err
	}

	c.logger.Info("Sandbox registered successfully",
		"sandboxID", regParams.SandboxID,
		"code", resp.Code,"message", resp.Message,
		"requestID", resp.RequestID)

	return resp, nil
}
```

```go
func executeGatewayRequest[T ResponseConstraint](
	ctx context.Context,
	c *AgentGatewayClient,
	sandboxID string,
	requestPath string,
	requestData interface{},
	builder EndpointGroupBuilder,
) (T, error) {

}
```

### 服务端返回

```go
type RegisterSandboxResponse struct {
	// Code Business response code, independent of HTTP status code.
	Code int `json:"code"`
	// Message is the response message
	Message string `json:"msg"`
	// RequestID is the unique identifier for the request
	RequestID string `json:"request_id"`
}

type UnregisterSandboxRequest struct {
	// SandboxID is the unique identifier of the sandbox
	SandboxID string `json:"sandbox_id"`
	// CellID is the identifier for the cell (required)
	CellID string `json:"cell_id"`
}
```

### 错误信息定义

1. 针对单个Endpoint的单次http request的Attempt，都会记录AttemptError
2. 针对单个Endpoint的请求，会返回AttemptError的数组，并基于这个数组的最后一次AttemptError，整合返回整体请求的Error信息

·```go
// AttemptError records error information for a single HTTP request attempt
type AttemptError struct {
    // Attempt number, starting from 1
    AttemptNumber int `json:"attempt_number"`

    // Request-ID for distributed tracing
    RequestID string `json:"request_id"`

    // Request endpoint address
    Endpoint string `json:"endpoint"`

    // Unified error code
    // - Client errors: use string enumeration (e.g., "client_config_error", "client_network_error", "client_timeout_error")
    // - Server errors: use string form of HTTP status code (e.g., "500", "404")
    UnifiedErrorCode int `json:"unified_error_code"`

    // Unified error information
    // - Client errors: contains specific network errors, timeout information, etc.
    // - Server errors: contains complete response body returned by the server
    UnifiedErrorDetail string `json:"unified_error_detail"`

    // Timestamp when the error occurred (RFC3339 format)
    Timestamp string `json:"timestamp"`

    // Error type
    ErrorType ErrorType `json:"error_type"`

}

// Error type constants
const (
// UnifiedCodeClientDefault is the fixed error code used for all client-side errors.
// For server-side errors, the UnifiedErrorCode will correspond to the actual HTTP status code.
UnifiedCodeClientDefault = 0
UnifiedUnexpectErrorCode = 1

    // Client error types
    ErrorTypeClientConfig  ErrorType = "client_config_error"  // Client configuration error
    ErrorTypeClientNetwork ErrorType = "client_network_error" // Client network error
    ErrorTypeClientRequest ErrorType = "client_request_error" // Client request error
    ErrorTypeClientTimeout ErrorType = "client_timeout_error" // Client timeout error

    // Server error types
    ErrorTypeServer4xx     ErrorType = "server_error_4xx" // Server 4xx business error
    ErrorTypeServer5xx     ErrorType = "server_error_5xx" // Server 5xx system error
    ErrorTypeServerUnknown ErrorType = "server_error_unknown"

    // Unhandled system error or non-HTTP error
    ErrorTypeUnexpectedError = "unexpected_error"

)

````

```go

func (e *MultiEndpointError) Error() string {
    // Guard clause: Handle empty attempts early
    if len(e.AttemptErrors) == 0 {
       return fmt.Sprintf("multi-endpoint error: no attempt details (total attempts: %d, group: %s)",
          e.TotalAttempts, e.GroupName)
    }

    last := e.AttemptErrors[len(e.AttemptErrors)-1]
    var msg string

    // Format the core error message based on type
    switch last.ErrorType {
    case ErrorTypeClientConfig:
       msg = "client configuration error: " + last.UnifiedErrorDetail
    case ErrorTypeClientNetwork:
       msg = "client network error: " + last.UnifiedErrorDetail
    case ErrorTypeClientTimeout:
       msg = "client timeout error: request timed out"
    case ErrorTypeServer4xx, ErrorTypeServer5xx, ErrorTypeServerUnknown:
       // Consolidate similar formatting logic for server/unknown errors
       prefix := "unknown error"
       if last.ErrorType == ErrorTypeServer4xx {
          prefix = "server 4xx error"
       } else if last.ErrorType == ErrorTypeServer5xx {
          prefix = "server 5xx error"
       }

       if last.UnifiedErrorCode > 0 {
          msg = fmt.Sprintf("%s: [%d] %s", prefix, last.UnifiedErrorCode, last.UnifiedErrorDetail)
       } else {
          msg = fmt.Sprintf("%s: %s", prefix, last.UnifiedErrorDetail)
       }
    default:
       msg = fmt.Sprintf("unhandled error type [%s]: %s", last.ErrorType, last.UnifiedErrorDetail)
    }

    // Centralized suffix: Attach metadata once at the end
    return fmt.Sprintf("%s (total attempts: %d, group: %s)", msg, e.TotalAttempts, e.GroupName)
}

````

## 本项目任务

### End2End测试用例防护

1. 以上示例代码展示了如何初始化多端点客户端，并使用它来进行多端点请求。
2. 当前我需要针对上述的实现，针对RegisterSandbox及同类的UnregisterSandbox的业务处理，进行End2End测试用例防护。
3. 关于多端点的定义，以及在多断点间进行请求重试的Policy策略配置参考如下：

```go

type ErrorType string

// RetryPolicy defines the retry strategy for a single endpoint
type RetryPolicy struct {
    MaxAttempts int           // Maximum number of attempts for the current address (1 means only initial request, no retry)
    Timeout     time.Duration // Timeout for a single request
    Interval    time.Duration // Backoff interval between attempts
}

// Endpoint represents a specific network access point and its associated access policy
type Endpoint struct {
    Domain string      // Domain name for TLS verification and DNS resolution (e.g., "example.com")
    IP     string      // IP address for direct connection (e.g., "192.168.1.1")
    Port   int         // Port number for connection (e.g., 8443, 443)
    Policy RetryPolicy // Retry configuration and timeout constraints specific to this access point
}

// EndpointGroup defines a collection of access points with sequential dependency
type EndpointGroup struct {
    Name      string     // Business logic name identifying this endpoint group, used for monitoring and log tracing
    Endpoints []Endpoint // Ordered slice of access points. Execution logic follows sequential failover mechanism
}
```

```go

// getRegisterEndpointGroup creates the endpoint group specifically for register operations.
// Policy: 5s timeout, 2 attempts for the primary endpoint, 1 attempt for backups.
func (c *AgentGatewayClient) getRegisterEndpointGroup(callbackAddresses []string) (retryablehttp.EndpointGroup, error) {
    const (
       registerTimeout = 5 * time.Second
       primaryAttempts = 2
       backupAttempts  = 1
    )

    policyFactory := func(index int) retryablehttp.RetryPolicy {
       maxAttempts := backupAttempts
       if index == 0 { // index 0 is the primary endpoint
          maxAttempts = primaryAttempts
       }

       return retryablehttp.RetryPolicy{
          MaxAttempts: maxAttempts,
          Timeout:     registerTimeout,
          Interval:    0,
       }
    }

    return c.buildEndpointGroup("agent-gateway-register", callbackAddresses, policyFactory)
}
```

4. End2End测试的E2E尽可能要覆盖如下场景：
   - 测试多端点请求的正常返回：首地址第一次请求成功，正常返回
   - 测试多端点请求的正常返回：首地址第一次请求失败，第二次重试请求成功。
   - 测试多端点请求的正常返回：首地址两次请求失败，按序重试到第二个地址后成功
   - 测试多端点请求的异常返回：首地址和第二个地址均明确错误返回。比如：第一个地址返回500，第二个地址返回404。
   - 测试多端点请求的超时返回：首地址和第二个地址的所有请求都超时，没有请求返回
   - 测试针对不同http status code的重试差异：429和408进行重试，其他4XX的请求不再同断点重试。相关重试逻辑，目前的业务代码已经实现，主要是模拟的服务端需要构造同类的场景。
   - 测试针对同一个断点，如果是客户端错误，如地址配置错误，导致请求超时，针对这种情况，不再进行重试。

### End2End测试用例构建策略

1. 上述过程中，涉及到Client的多次请求发送，和服务端多Endpoint的模拟。客户端和服务端都可以通过容器进行管理。
2. 测试端可以集成当前的业务代码，进行简单Mock修改后，作为客户端，每次请求根据用例编号，动态拉起客户端容器。
3. 需要支持通过策略文件进行客户端和服务端的管理，服务端需要在覆盖前置所有测试场景的情况下，完成动态的客户端和服务端容器的拉起。
4. 所有测试过程的触发，通过run-e2e.sh脚本进行触发。脚本Ene2End测试用例的策略文件，动态拉起客户端和服务端容器

### 输出要求

1. 基于以上要求，实现一个可以动态拉起客户端和服务端容器的脚本。
2. 实现不同策略下的，服务端容器代码的实现。返回满足条件的HTTP请求返回。
3. 客户端代码会申请对应的AgentClient，并打桩callbackAddress进行请求。在End2End的测试过程中，当前业务的代码主要会是客户端代码，所以会尽可实现客户端代码的复用。
