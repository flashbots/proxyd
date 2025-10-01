package proxyd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func TestStripXFF(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"1.2.3, 4.5.6, 7.8.9", "1.2.3"},
		{"1.2.3,4.5.6", "1.2.3"},
		{" 1.2.3 , 4.5.6 ", "1.2.3"},
	}

	for _, test := range tests {
		actual := stripXFF(test.in)
		assert.Equal(t, test.out, actual)
	}
}

func TestLimitedHTTPClientDoLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("unlimited requests", func(t *testing.T) {
		client := &LimitedHTTPClient{
			Client:      http.Client{},
			sem:         nil,
			backendName: "test-unlimited",
		}

		req, err := http.NewRequest("GET", server.URL, nil)
		require.NoError(t, err)

		resp, err := client.DoLimited(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("limited requests", func(t *testing.T) {
		sem := semaphore.NewWeighted(1)
		client := &LimitedHTTPClient{
			Client:      http.Client{},
			sem:         sem,
			backendName: "test-limited",
		}

		req, err := http.NewRequest("GET", server.URL, nil)
		require.NoError(t, err)

		resp, err := client.DoLimited(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Exhaust semaphore
		require.True(t, sem.TryAcquire(1))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		req, err = http.NewRequestWithContext(ctx, "GET", server.URL, nil)
		require.NoError(t, err)

		resp, err = client.DoLimited(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many requests")
		require.Nil(t, resp)
	})
}

func TestClientDisconnectionFlow499(t *testing.T) {
	initialCount := getHttpResponseCodeCount("499")

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			// Context cancelled - return immediately to simulate backend detecting cancellation
			return
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1","id":1}`))
		}
	}))
	defer backendServer.Close()

	backend := NewBackend("test-backend", backendServer.URL, "", semaphore.NewWeighted(1))
	backendGroup := &BackendGroup{
		Name:     "test-group",
		Backends: []*Backend{backend},
	}

	rpcMethodMappings := map[string]string{
		"eth_blockNumber": "test-group",
	}

	proxydServer, err := NewServer(
		map[string]*BackendGroup{"test-group": backendGroup},
		backendGroup,
		NewStringSetFromStrings([]string{"eth_blockNumber"}),
		rpcMethodMappings,
		1024*1024,           // maxBodySize
		map[string]string{}, // authenticatedPaths
		5*time.Second,       // timeout - longer than our test
		10,                  // maxUpstreamBatchSize
		false,               // enableServedByHeader
		&NoopRPCCache{},     // cache
		RateLimitConfig{},   // rateLimitConfig
		RateLimitConfig{},
		make(map[common.Address]bool),
		SenderRateLimitConfig{}, // senderRateLimitConfig
		SenderRateLimitConfig{}, // interopSenderRateLimitConfig
		false,                   // enableRequestLog
		0,                       // maxRequestBodyLogLen
		100,                     // maxBatchSize
		func(dur time.Duration, max int, prefix string) FrontendRateLimiter {
			return NoopFrontendRateLimiter
		}, // limiterFactory
		InteropValidationConfig{},              // interopValidatingConfig
		NewFirstSupervisorStrategy([]string{}), // interopStrategy
		[]string{},
		false,
	)
	require.NoError(t, err)

	reqBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`

	httpReq := httptest.NewRequest("POST", "/", strings.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Forwarded-For", "127.0.0.1")

	ctx, cancel := context.WithCancel(httpReq.Context())
	httpReq = httpReq.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Start the request in a goroutine and cancel it immediately to simulate client disconnection
	done := make(chan bool)
	go func() {
		defer func() { done <- true }()
		proxydServer.HandleRPC(rr, httpReq)
	}()

	cancel()
	<-done

	t.Logf("Response status code: %d", rr.Code)

	finalCount := getHttpResponseCodeCount("499")

	assert.Greater(t, finalCount, initialCount, "httpResponseCodesTotal should be incremented for 499 status code")
}

func getHttpResponseCodeCount(statusCode string) float64 {
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "proxyd_http_response_codes_total" {
			for _, metric := range mf.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "status_code" && label.GetValue() == statusCode {
						return metric.GetCounter().GetValue()
					}
				}
			}
		}
	}
	return 0
}

func TestHeaderForwarding(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		query         string
		expectedHeaders map[string]string
	}{
		{
			name:  "fast path forwarded as header",
			path:  "/fast",
			query: "",
			expectedHeaders: map[string]string{
				"X-Original-Path": "/fast",
			},
		},
		{
			name:  "fast path with query forwarded as headers",
			path:  "/fast",
			query: "hint=calldata&builder=flashbots",
			expectedHeaders: map[string]string{
				"X-Original-Path":  "/fast",
				"X-Original-Query": "hint=calldata&builder=flashbots",
			},
		},
		{
			name:  "root path with query forwarded as headers",
			path:  "/",
			query: "builder=rsync",
			expectedHeaders: map[string]string{
				"X-Original-Query": "builder=rsync",
			},
		},
		{
			name:  "query only forwarded as header",
			path:  "",
			query: "hint=hash",
			expectedHeaders: map[string]string{
				"X-Original-Query": "hint=hash",
			},
		},
		{
			name:            "no path or query means no headers",
			path:            "",
			query:           "",
			expectedHeaders: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with headers to forward (unified approach)
			ctx := context.Background()
			headersToForward := make(map[string][]string)

			if tt.path != "" && tt.path != "/" {
				headersToForward["X-Original-Path"] = []string{tt.path}
			}
			if tt.query != "" {
				headersToForward["X-Original-Query"] = []string{tt.query}
			}

			if len(headersToForward) > 0 {
				ctx = context.WithValue(ctx, ContextKeyHeadersToForward, headersToForward)
			}

			// Create a mock HTTP request to verify headers are set
			req, err := http.NewRequestWithContext(ctx, "POST", "http://backend:8080", nil)
			require.NoError(t, err)

			// Simulate the header forwarding logic from doForward()
			headersToForwardFromCtx := GetHeadersToForward(ctx)
			for k, v := range headersToForwardFromCtx {
				for _, value := range v {
					req.Header.Add(k, value)
				}
			}

			// Verify the expected headers are set correctly
			for expectedHeader, expectedValue := range tt.expectedHeaders {
				actualValue := req.Header.Get(expectedHeader)
				assert.Equal(t, expectedValue, actualValue, "Header %s should have value %s", expectedHeader, expectedValue)
			}

			// Verify no unexpected headers are set
			if _, exists := tt.expectedHeaders["X-Original-Path"]; !exists {
				assert.Empty(t, req.Header.Get("X-Original-Path"), "X-Original-Path should not be set")
			}
			if _, exists := tt.expectedHeaders["X-Original-Query"]; !exists {
				assert.Empty(t, req.Header.Get("X-Original-Query"), "X-Original-Query should not be set")
			}
		})
	}
}

