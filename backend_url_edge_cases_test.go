package proxyd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBackendURLEdgeCases covers all the tricky slash/query edge cases
func TestBackendURLEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		method   string
		path     string
		query    string
		expected string
	}{
		// === SLASH EDGE CASES ===
		{
			name:     "base URL with trailing slash + path",
			baseURL:  "http://backend:8080/",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "",
			expected: "http://backend:8080/fast", // Should NOT be //fast
		},
		{
			name:     "base URL with trailing slash + path + query",
			baseURL:  "http://backend:8080/",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "hint=hash",
			expected: "http://backend:8080/fast?hint=hash",
		},
		{
			name:     "path is just slash with query",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "/",
			query:    "hint=hash",
			expected: "http://backend:8080?hint=hash", // Should NOT add slash before ?
		},

		// === QUERY STRING EDGE CASES ===
		{
			name:     "multiple query parameters",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "hint=hash&builder=builder1&origin=wallet",
			expected: "http://backend:8080/fast?hint=hash&builder=builder1&origin=wallet",
		},
		{
			name:     "query with URL-encoded special chars",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "hint=0x1234%20test&url=https%3A%2F%2Fexample.com",
			expected: "http://backend:8080/fast?hint=0x1234%20test&url=https%3A%2F%2Fexample.com",
		},
		{
			name:     "query with equals in value",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "signature=0xabc=def",
			expected: "http://backend:8080/fast?signature=0xabc=def",
		},

		// === EMPTY/MISSING EDGE CASES ===
		{
			name:     "empty path string",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "",
			query:    "hint=hash",
			expected: "http://backend:8080?hint=hash", // Empty path should be treated like "/"
		},
		{
			name:     "empty query string",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "",
			expected: "http://backend:8080/fast", // Should NOT add ? for empty query
		},
		{
			name:     "both path and query empty",
			baseURL:  "http://backend:8080",
			method:   "eth_sendRawTransaction",
			path:     "",
			query:    "",
			expected: "http://backend:8080",
		},

		// === METHOD FILTERING ===
		{
			name:     "non-eth_sendRawTransaction ignores everything",
			baseURL:  "http://backend:8080",
			method:   "eth_call",
			path:     "/fast",
			query:    "hint=hash&builder=builder1",
			expected: "http://backend:8080", // Should completely ignore path and query
		},

		// === PRODUCTION-LIKE SCENARIOS ===
		{
			name:     "real production URL with everything",
			baseURL:  "http://rpc-endpoint.flashbots.svc.cluster.local:8080",
			method:   "eth_sendRawTransaction",
			path:     "/fast",
			query:    "hint=0xabcdef1234567890&builder=flashbots&origin=metamask",
			expected: "http://rpc-endpoint.flashbots.svc.cluster.local:8080/fast?hint=0xabcdef1234567890&builder=flashbots&origin=metamask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = context.WithValue(ctx, ContextKeyPath, tt.path)
			ctx = context.WithValue(ctx, ContextKeyRawQuery, tt.query)

			rpcReqs := []*RPCReq{{Method: tt.method}}
			backendURL := buildBackendURL(tt.baseURL, rpcReqs, ctx)

			assert.Equal(t, tt.expected, backendURL, "URL mismatch for case: %s", tt.name)
		})
	}
}

// TestBackendURLBoundaryConditions tests weird inputs that shouldn't happen but might
func TestBackendURLBoundaryConditions(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		rpcReqs  []*RPCReq
		path     string
		query    string
		expected string
	}{
		{
			name:     "empty rpcReqs array",
			baseURL:  "http://backend:8080",
			rpcReqs:  []*RPCReq{},
			path:     "/fast",
			query:    "hint=hash",
			expected: "http://backend:8080", // Should not panic, just return base
		},
		{
			name:     "nil rpcReqs array",
			baseURL:  "http://backend:8080",
			rpcReqs:  nil,
			path:     "/fast",
			query:    "hint=hash",
			expected: "http://backend:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = context.WithValue(ctx, ContextKeyPath, tt.path)
			ctx = context.WithValue(ctx, ContextKeyRawQuery, tt.query)

			// Should not panic
			backendURL := buildBackendURL(tt.baseURL, tt.rpcReqs, ctx)
			assert.Equal(t, tt.expected, backendURL)
		})
	}
}
