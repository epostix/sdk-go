package epostix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStuckCursorIsCaughtOnTheSecondRequest(t *testing.T) {
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"e1"}],"has_more":true,"next_cursor":"STUCK"}`))
	}))
	defer server.Close()

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	for iterator.Next() {
	}

	if iterator.Err() == nil {
		t.Fatal("expected a RepeatedCursor error")
	}

	if requests != 2 {
		t.Fatalf("a stuck cursor must be caught on the second request, took %d", requests)
	}
}
