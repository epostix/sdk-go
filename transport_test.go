package epostix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	base := []Option{
		WithBaseURL(server.URL),
		WithRetryPolicy(DisabledRetryPolicy()),
	}

	return New("tix_test_abc", append(base, opts...)...), server
}

func writeError(w http.ResponseWriter, status int, errType string, headers map[string]string) {
	for name, value := range headers {
		w.Header().Set(name, value)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(map[string]any{
		"status": status, "type": errType, "message": "failure", "request_id": "req_1",
	})
}

func TestUnknownErrorTypeDegradesByStatus(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 503, "quantum_overload", nil)
	})

	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an APIError, got %v", err)
	}

	if apiErr.KnownType {
		t.Error("an unrecognised type must be marked unknown")
	}

	if string(apiErr.Type) != "quantum_overload" {
		t.Errorf("the raw type must be preserved, got %s", apiErr.Type)
	}

	if apiErr.Kind != KindServer {
		t.Errorf("a 503 must classify as a server error, got %s", apiErr.Kind)
	}

	if !errors.Is(err, ErrServer) {
		t.Error("errors.Is must match the server sentinel")
	}
}

func TestSentinelsAreDetectableByIdentity(t *testing.T) {
	cases := []struct {
		errType  string
		status   int
		sentinel error
	}{
		{"rate_limit_exceeded", 429, ErrRateLimited},
		{"daily_quota_exceeded", 429, ErrQuotaExceeded},
		{"idempotency_conflict", 409, ErrIdempotencyConflict},
		{"validation_error", 422, ErrValidation},
		{"authentication_failed", 401, ErrAuthentication},
		{"not_found", 404, ErrNotFound},
		{"workspace_suspended", 403, ErrSuspended},
	}

	for _, testCase := range cases {
		t.Run(testCase.errType, func(t *testing.T) {
			client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeError(w, testCase.status, testCase.errType, nil)
			})

			_, err := client.Emails.GetEmail(context.Background(), "eml_1")

			if !errors.Is(err, testCase.sentinel) {
				t.Errorf("errors.Is failed for %s: %v", testCase.errType, err)
			}
		})
	}
}

func TestRateLimitHeadersAreParsed(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 429, "rate_limit_exceeded", map[string]string{
			"RateLimit-Limit": "100", "RateLimit-Remaining": "0", "RateLimit-Reset": "2",
			"RateLimit-Policy": "100;w=60", "RateLimit-Scope": "workspace", "Retry-After": "2",
		})
	})

	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an APIError, got %v", err)
	}

	snapshot := apiErr.Meta.RateLimit
	if !snapshot.Present || snapshot.Limit != 100 || snapshot.Remaining != 0 {
		t.Errorf("rate limit snapshot = %#v", snapshot)
	}

	if snapshot.PolicyBurst != 100 || snapshot.PolicyWindow != time.Minute {
		t.Errorf("policy = %d;w=%v", snapshot.PolicyBurst, snapshot.PolicyWindow)
	}

	if apiErr.RetryAfter != 2*time.Second {
		t.Errorf("retryAfter = %v", apiErr.RetryAfter)
	}
}

func TestMonthlyQuotaHasNoNextAttempt(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 429, "monthly_quota_exceeded", nil)
	})

	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	var apiErr *APIError
	errors.As(err, &apiErr)

	if apiErr.QuotaPeriod != QuotaPeriodMonthly {
		t.Errorf("quotaPeriod = %s", apiErr.QuotaPeriod)
	}

	if apiErr.RetryAfter != 0 {
		t.Errorf("a monthly quota failure must not invent a retry-after, got %v", apiErr.RetryAfter)
	}
}

func TestDailyQuotaNeverSleeps(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 429, "daily_quota_exceeded", map[string]string{"Retry-After": "31200"})
	}, WithRetryPolicy(RetryPolicy{
		MaximumRetries: 3, OperationDeadline: 30 * time.Second, ConnectTimeout: time.Second,
		ReadTimeout: 5 * time.Second, BackoffBase: time.Millisecond, BackoffMaximum: time.Millisecond,
		BackoffMultiplier: 2, HonorRetryAfter: true, MutationRetry: MutationRetryOnlyWhenDeduplicated,
	}))

	started := time.Now()
	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	if time.Since(started) > 2*time.Second {
		t.Fatal("the SDK must not sleep a daily quota retry-after")
	}

	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("expected a quota error, got %v", err)
	}
}

func TestNonJSONProxyFailureIsBounded(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
	})

	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an APIError, got %v", err)
	}

	if apiErr.Type != "" || apiErr.KnownType {
		t.Errorf("a non-JSON body must yield an empty unknown type, got %q", apiErr.Type)
	}

	if !strings.Contains(apiErr.RawBodySnippet, "502 Bad Gateway") {
		t.Error("the raw body snippet must be reachable")
	}

	if strings.Contains(apiErr.Error(), "<html>") {
		t.Error("the raw body must not leak into the error string")
	}
}

func TestAutomaticIdempotencyKeyOnDeduplicatedSend(t *testing.T) {
	var seen string

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "eml_1"})
	})

	_, err := client.Emails.SendEmail(context.Background(), &EmailCreate{
		From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if !strings.HasPrefix(seen, "epx_auto_") || len(seen) != len("epx_auto_")+32 {
		t.Errorf("expected an auto-generated key, got %q", seen)
	}
}

func TestIdempotencyKeyRejectedOnExcludedOperation(t *testing.T) {
	requests := 0

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(201)
	})

	_, err := client.APIKeys.CreateAPIKey(context.Background(),
		&APIKeyCreateRequest{Name: "ci"}, WithIdempotencyKey("k"))

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected a ConfigError, got %v", err)
	}

	if requests != 0 {
		t.Error("no request may be sent when the key is rejected")
	}
}

func TestRetryReplaysIdenticalBytesAndKey(t *testing.T) {
	var (
		mu     sync.Mutex
		keys   []string
		bodies []string
	)

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, r.ContentLength)
		r.Body.Read(buffer)

		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		bodies = append(bodies, string(buffer))
		attempt := len(keys)
		mu.Unlock()

		if attempt == 1 {
			writeError(w, 503, "service_unavailable", nil)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "eml_1"})
	}, WithRetryPolicy(RetryPolicy{
		MaximumRetries: 2, OperationDeadline: 20 * time.Second, ConnectTimeout: time.Second,
		ReadTimeout: 5 * time.Second, BackoffBase: time.Millisecond, BackoffMaximum: 2 * time.Millisecond,
		BackoffMultiplier: 2, HonorRetryAfter: true, MutationRetry: MutationRetryOnlyWhenDeduplicated,
	}))

	_, err := client.Emails.SendEmail(context.Background(), &EmailCreate{
		From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(keys))
	}

	if keys[0] != keys[1] {
		t.Error("a retry must reuse the same idempotency key")
	}

	if bodies[0] != bodies[1] {
		t.Error("a retry must replay identical bytes")
	}
}

func TestValidationFailureIsNeverRetried(t *testing.T) {
	requests := 0

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeError(w, 422, "validation_error", nil)
	}, WithRetryPolicy(RetryPolicy{
		MaximumRetries: 3, OperationDeadline: 20 * time.Second, ConnectTimeout: time.Second,
		ReadTimeout: 5 * time.Second, BackoffBase: time.Millisecond, BackoffMaximum: time.Millisecond,
		BackoffMultiplier: 2, HonorRetryAfter: true, MutationRetry: MutationRetryOnlyWhenDeduplicated,
	}))

	client.Emails.SendEmail(context.Background(), &EmailCreate{
		From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s",
	})

	if requests != 1 {
		t.Errorf("a validation failure must not be retried, saw %d requests", requests)
	}
}

func TestLostResponseReportsUnknownOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}

		conn, _, _ := hijacker.Hijack()
		conn.Close()
	}))
	t.Cleanup(server.Close)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	_, err := client.Emails.SendEmail(context.Background(), &EmailCreate{
		From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s",
	})

	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected an OutcomeUnknownError, got %T %v", err, err)
	}

	if unknown.Operation != "sendEmail" {
		t.Errorf("operation = %s", unknown.Operation)
	}

	if !unknown.ReplayableWithKey {
		t.Error("a deduplicated send with a key must be reported replayable")
	}
}

func TestCancellationStopsBeforeAnyRequest(t *testing.T) {
	requests := 0

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Emails.GetEmail(ctx, "eml_1")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	if requests != 0 {
		t.Error("no request may be sent after cancellation")
	}
}

func TestResponseMetadataIsReachable(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("X-Request-Id", "req_42")
		json.NewEncoder(w).Encode(map[string]any{"id": "eml_1"})
	})

	response, err := client.Do(context.Background(), &RawRequest{Method: "GET", Path: "/emails/eml_1"})
	if err != nil {
		t.Fatalf("raw request: %v", err)
	}
	defer response.Body.Close()

	if !response.Meta.IdempotentReplayed {
		t.Error("the replay flag must be surfaced")
	}

	if response.Meta.RequestID != "req_42" {
		t.Errorf("requestID = %s", response.Meta.RequestID)
	}
}

func TestConfigurationErrorsSurfaceOnFirstCall(t *testing.T) {
	client := New("Bearer tix_live_x")

	_, err := client.Emails.GetEmail(context.Background(), "eml_1")

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected a ConfigError, got %v", err)
	}
}

func TestKeyEnvironmentIsReadableWithoutNetwork(t *testing.T) {
	cases := map[string]KeyEnvironment{
		"tix_live_x": KeyEnvironmentLive,
		"tix_test_x": KeyEnvironmentTest,
		"tix_x":      KeyEnvironmentUnrecognized,
	}

	for key, want := range cases {
		if got := New(key).KeyEnvironment(); got != want {
			t.Errorf("%s => %s, want %s", key, got, want)
		}
	}
}

func TestClientsAreSafeForConcurrentUse(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q}`, r.URL.Path)
	})

	var group sync.WaitGroup

	for index := 0; index < 32; index++ {
		group.Add(1)

		go func(n int) {
			defer group.Done()

			if _, err := client.Emails.GetEmail(context.Background(), fmt.Sprintf("eml_%d", n)); err != nil {
				t.Errorf("concurrent call: %v", err)
			}
		}(index)
	}

	group.Wait()
}

func TestDerivedClientDoesNotShareCredentials(t *testing.T) {
	var seen sync.Map

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"), true)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"eml_1"}`))
	})

	other := client.WithAPIKey("tix_test_second")

	client.Emails.GetEmail(context.Background(), "eml_1")
	other.Emails.GetEmail(context.Background(), "eml_1")

	if _, ok := seen.Load("Bearer tix_test_abc"); !ok {
		t.Error("the original credential must be used by the original client")
	}

	if _, ok := seen.Load("Bearer tix_test_second"); !ok {
		t.Error("the derived credential must be used by the derived client")
	}
}
