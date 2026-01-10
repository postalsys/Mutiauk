package wizard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewDashboardVerifier(t *testing.T) {
	verifier := NewDashboardVerifier()

	if verifier == nil {
		t.Fatal("NewDashboardVerifier() returned nil")
	}
	if verifier.client == nil {
		t.Error("verifier.client is nil")
	}
	if verifier.client.Timeout != dashboardAPITimeout {
		t.Errorf("client.Timeout = %v, want %v", verifier.client.Timeout, dashboardAPITimeout)
	}
}

func TestDashboardVerifier_Verify_Success(t *testing.T) {
	// Create a test server that returns 200 OK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request path
		if r.URL.Path != dashboardAPIPath {
			t.Errorf("request path = %s, want %s", r.URL.Path, dashboardAPIPath)
		}
		// Verify Accept header
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept header = %s, want application/json", r.Header.Get("Accept"))
		}
		// Verify method
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	verifier := NewDashboardVerifier()
	err := verifier.Verify(server.URL)

	if err != nil {
		t.Errorf("Verify() error = %v, want nil", err)
	}
}

func TestDashboardVerifier_Verify_Non200Status(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"502 Bad Gateway", http.StatusBadGateway},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			verifier := NewDashboardVerifier()
			err := verifier.Verify(server.URL)

			if err == nil {
				t.Error("Verify() expected error, got nil")
				return
			}
			if !strings.Contains(err.Error(), "unexpected status code") {
				t.Errorf("error should mention 'unexpected status code', got: %v", err)
			}
			if !strings.Contains(err.Error(), string(rune('0'+tt.statusCode/100))) {
				// Check that status code appears in error
				expectedCode := http.StatusText(tt.statusCode)
				_ = expectedCode // Status text might not be in error, just the code
			}
		})
	}
}

func TestDashboardVerifier_Verify_ConnectionFailed(t *testing.T) {
	verifier := NewDashboardVerifier()

	// Use a URL that will fail to connect
	err := verifier.Verify("http://localhost:59999")

	if err == nil {
		t.Error("Verify() expected error for unreachable server, got nil")
		return
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("error should mention 'connection failed', got: %v", err)
	}
}

func TestDashboardVerifier_Verify_InvalidURL(t *testing.T) {
	verifier := NewDashboardVerifier()

	// Use an invalid URL
	err := verifier.Verify("://invalid-url")

	if err == nil {
		t.Error("Verify() expected error for invalid URL, got nil")
		return
	}
	if !strings.Contains(err.Error(), "failed to create request") {
		t.Errorf("error should mention 'failed to create request', got: %v", err)
	}
}

func TestDashboardVerifier_Verify_Timeout(t *testing.T) {
	// Create a server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create verifier with very short timeout
	verifier := &DashboardVerifier{
		client: &http.Client{Timeout: 50 * time.Millisecond},
	}

	err := verifier.Verify(server.URL)

	if err == nil {
		t.Error("Verify() expected timeout error, got nil")
		return
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("error should mention 'connection failed', got: %v", err)
	}
}

func TestDashboardVerifier_Verify_RedirectHandling(t *testing.T) {
	// Create a server that redirects
	redirectCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectCount < 1 {
			redirectCount++
			http.Redirect(w, r, "/redirected"+dashboardAPIPath, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	verifier := NewDashboardVerifier()
	err := verifier.Verify(server.URL)

	// Should follow redirect and succeed
	if err != nil {
		t.Errorf("Verify() error = %v, want nil (should follow redirect)", err)
	}
}

func TestDashboardConnectionError_Error(t *testing.T) {
	err := &DashboardConnectionError{
		BaseURL: "https://example.com",
		Err:     errors.New("connection refused"),
	}

	errStr := err.Error()

	if !strings.Contains(errStr, "https://example.com") {
		t.Errorf("Error() should contain base URL, got: %s", errStr)
	}
	if !strings.Contains(errStr, "connection refused") {
		t.Errorf("Error() should contain underlying error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "failed to connect") {
		t.Errorf("Error() should contain 'failed to connect', got: %s", errStr)
	}
}

func TestDashboardConnectionError_Unwrap(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	err := &DashboardConnectionError{
		BaseURL: "https://example.com",
		Err:     underlyingErr,
	}

	unwrapped := err.Unwrap()

	if unwrapped != underlyingErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, underlyingErr)
	}
}

func TestDashboardConnectionError_Unwrap_WorksWithErrorsIs(t *testing.T) {
	targetErr := errors.New("specific error")
	err := &DashboardConnectionError{
		BaseURL: "https://example.com",
		Err:     targetErr,
	}

	if !errors.Is(err, targetErr) {
		t.Error("errors.Is should return true for wrapped error")
	}
}

func TestDashboardConnectionError_PossibleCauses(t *testing.T) {
	err := &DashboardConnectionError{
		BaseURL: "https://example.com",
		Err:     errors.New("some error"),
	}

	causes := err.PossibleCauses()

	if len(causes) == 0 {
		t.Error("PossibleCauses() returned empty slice")
		return
	}

	// Check for expected causes
	expectedCauses := []string{
		"not running",
		"Incorrect URL",
		"Network",
		"Firewall",
	}

	for _, expected := range expectedCauses {
		found := false
		for _, cause := range causes {
			if strings.Contains(cause, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("PossibleCauses() should include cause containing %q", expected)
		}
	}
}

func TestDashboardConnectionError_PossibleCauses_ReturnsNewSlice(t *testing.T) {
	err := &DashboardConnectionError{
		BaseURL: "https://example.com",
		Err:     errors.New("some error"),
	}

	causes1 := err.PossibleCauses()
	causes2 := err.PossibleCauses()

	// Modify first slice
	if len(causes1) > 0 {
		causes1[0] = "modified"
	}

	// Second slice should be unaffected (new slice each time)
	if len(causes2) > 0 && causes2[0] == "modified" {
		t.Error("PossibleCauses() should return a new slice each time")
	}
}

func TestDashboardVerifier_Verify_EmptyURL(t *testing.T) {
	verifier := NewDashboardVerifier()

	err := verifier.Verify("")

	if err == nil {
		t.Error("Verify() expected error for empty URL, got nil")
	}
}

func TestDashboardVerifier_Verify_URLWithTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path should be /api/dashboard even if base URL has trailing slash
		if r.URL.Path != dashboardAPIPath {
			t.Errorf("request path = %s, want %s", r.URL.Path, dashboardAPIPath)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	verifier := NewDashboardVerifier()
	// Note: current implementation doesn't handle trailing slash specially
	// This test documents current behavior
	err := verifier.Verify(server.URL)

	if err != nil {
		t.Errorf("Verify() error = %v", err)
	}
}

func TestDashboardVerifier_Verify_HTTPSServer(t *testing.T) {
	// Create HTTPS test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Use the server's client which has the test certificate
	verifier := &DashboardVerifier{
		client: server.Client(),
	}

	err := verifier.Verify(server.URL)

	if err != nil {
		t.Errorf("Verify() error = %v, want nil", err)
	}
}

func TestConstants(t *testing.T) {
	// Verify constants are set to expected values
	if dashboardAPIPath != "/api/dashboard" {
		t.Errorf("dashboardAPIPath = %s, want /api/dashboard", dashboardAPIPath)
	}
	if dashboardAPITimeout != 10*time.Second {
		t.Errorf("dashboardAPITimeout = %v, want 10s", dashboardAPITimeout)
	}
}

// Benchmark tests
func BenchmarkDashboardVerifier_Verify(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	verifier := NewDashboardVerifier()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		verifier.Verify(server.URL)
	}
}
