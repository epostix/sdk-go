package epostix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTheRawInboundPathCarriesNoIdempotencyKey(t *testing.T) {
	var seen http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("Content-Type", "message/rfc822")
		w.Write([]byte("From: a@b\r\n\r\nbody"))
	}))
	defer server.Close()

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	content, err := client.Inbound.GetInboundEmailRawBytes(context.Background(), "inb_1", 1000)
	if err != nil {
		t.Fatalf("raw fetch: %v", err)
	}

	if len(content) == 0 {
		t.Fatal("expected the raw message bytes")
	}

	if key := seen.Get("Idempotency-Key"); key != "" {
		t.Fatalf("a streaming GET must not carry an idempotency key, got %q", key)
	}
}

func TestTheRawByteCapIsRequiredAndEnforced(t *testing.T) {
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "message/rfc822")
		w.Write(make([]byte, 512))
	}))
	defer server.Close()

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	if _, err := client.Inbound.GetInboundEmailRawBytes(context.Background(), "inb_1", 0); err == nil {
		t.Fatal("a non-positive cap must be rejected")
	}

	if requests != 0 {
		t.Fatal("a rejected cap must not reach the network")
	}

	if _, err := client.Inbound.GetInboundEmailRawBytes(context.Background(), "inb_1", 64); err == nil {
		t.Fatal("a message larger than the cap must be rejected")
	}
}

func TestAnOversizeUploadNeverReachesTheNetwork(t *testing.T) {
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	oversize := make([]byte, AttachmentDecodedLimitBytes+1)

	if _, err := client.Attachments.UploadAttachment(
		context.Background(), "huge.bin", oversize, "application/octet-stream",
	); err == nil {
		t.Fatal("an oversize upload must be rejected locally")
	}

	if requests != 0 {
		t.Fatalf("the size projection must run before any network call, saw %d requests", requests)
	}
}
