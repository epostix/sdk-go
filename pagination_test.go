package epostix

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func pagedServer(t *testing.T, pages []string, seen *[]url.Values) *httptest.Server {
	t.Helper()

	index := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Query())

		page := pages[len(pages)-1]
		if index < len(pages) {
			page = pages[index]
		}

		index++

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(page))
	}))

	t.Cleanup(server.Close)

	return server
}

func TestIteratorPersistsFilters(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1"}],"has_more":true,"next_cursor":"e1"}`,
		`{"data":[{"id":"e2"}],"has_more":false}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	status := ListEmailsStatusDelivered
	limit := 1

	iterator := client.Emails.ListEmailsIterator(context.Background(), &ListEmailsParams{
		Status: &status, Limit: &limit,
	})

	var ids []string
	for iterator.Next() {
		ids = append(ids, iterator.Current().ID)
	}

	if err := iterator.Err(); err != nil {
		t.Fatalf("iteration: %v", err)
	}

	if len(ids) != 2 || ids[0] != "e1" || ids[1] != "e2" {
		t.Fatalf("ids = %v", ids)
	}

	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}

	if seen[1].Get("status") != "delivered" {
		t.Error("filters must persist onto the second page")
	}

	if seen[1].Get("starting_after") != "e1" {
		t.Errorf("starting_after = %q", seen[1].Get("starting_after"))
	}
}

func TestIteratorEarlyBreakStopsFetching(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1"},{"id":"e2"}],"has_more":true,"next_cursor":"e2"}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	for iterator.Next() {
		break
	}

	if len(seen) != 1 {
		t.Errorf("an early break must not fetch another page, saw %d requests", len(seen))
	}
}

func TestIteratorReportsMissingCursor(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1"}],"has_more":true}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	for iterator.Next() {
	}

	var pageErr *PaginationError
	if !errors.As(iterator.Err(), &pageErr) || pageErr.Reason != ReasonMissingCursor {
		t.Fatalf("expected a MissingCursor error, got %v", iterator.Err())
	}
}

func TestIteratorReportsRepeatedCursor(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1"}],"has_more":true,"next_cursor":"c1"}`,
		`{"data":[{"id":"e2"}],"has_more":true,"next_cursor":"c1"}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	for iterator.Next() {
	}

	var pageErr *PaginationError
	if !errors.As(iterator.Err(), &pageErr) || pageErr.Reason != ReasonRepeatedCursor {
		t.Fatalf("expected a RepeatedCursor error, got %v", iterator.Err())
	}
}

func TestCollectRequiresACapAndReportsTruncation(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1"},{"id":"e2"},{"id":"e3"}],"has_more":true,"next_cursor":"e3"}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	if _, err := client.Emails.ListEmailsIterator(context.Background(), nil).Collect(0); err == nil {
		t.Fatal("Collect must reject a non-positive cap")
	}

	collected, err := client.Emails.ListEmailsIterator(context.Background(), nil).Collect(2)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if len(collected.Items) != 2 || !collected.Truncated {
		t.Errorf("collected = %d items truncated=%v", len(collected.Items), collected.Truncated)
	}
}

func TestEmptyIntermediatePageContinues(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[],"has_more":true,"next_cursor":"c1"}`,
		`{"data":[{"id":"e1"}],"has_more":false}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	var ids []string
	for iterator.Next() {
		ids = append(ids, iterator.Current().ID)
	}

	if err := iterator.Err(); err != nil {
		t.Fatalf("iteration: %v", err)
	}

	if len(ids) != 1 || ids[0] != "e1" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestTemplateVersionsPaginateForward(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"version":3}],"has_more":true,"next_cursor":"3"}`,
		`{"data":[{"version":2}],"has_more":false}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Templates.Versions.ListTemplateVersionsIterator(context.Background(), "tpl_1", nil)

	count := 0
	for iterator.Next() {
		count++
	}

	if err := iterator.Err(); err != nil {
		t.Fatalf("iteration: %v", err)
	}

	if count != 2 {
		t.Fatalf("expected 2 versions, got %d", count)
	}

	if seen[1].Get("starting_after") != "3" {
		t.Errorf("template versions must follow the cursor, got %q", seen[1].Get("starting_after"))
	}
}

func TestIteratorRejectsEndingBefore(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{`{"data":[],"has_more":false}`}, &seen)
	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))

	before := "e9"
	iterator := client.Emails.ListEmailsIterator(context.Background(), &ListEmailsParams{EndingBefore: &before})

	if iterator.Next() {
		t.Fatal("an iterator with ending_before must not yield")
	}

	var configErr *ConfigError
	if !errors.As(iterator.Err(), &configErr) {
		t.Fatalf("expected a ConfigError, got %v", iterator.Err())
	}
}

func TestPageDecodeSurvivesUnknownFields(t *testing.T) {
	var seen []url.Values

	server := pagedServer(t, []string{
		`{"data":[{"id":"e1","future_field":42}],"has_more":false,"future_top":true}`,
	}, &seen)

	client := New("tix_test_abc", WithBaseURL(server.URL), WithRetryPolicy(DisabledRetryPolicy()))
	iterator := client.Emails.ListEmailsIterator(context.Background(), nil)

	count := 0
	for iterator.Next() {
		count++
	}

	if err := iterator.Err(); err != nil {
		t.Fatalf("unknown fields must not fail decoding: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 item, got %d", count)
	}
}

var _ = json.Marshal
