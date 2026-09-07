package epostix

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RateLimitSnapshot struct {
	Limit        int
	Remaining    int
	ResetAfter   time.Duration
	PolicyBurst  int
	PolicyWindow time.Duration
	Scope        string
	RawPolicy    string
	Present      bool
}

type AttemptRecord struct {
	AttemptNumber int
	StartedAt     time.Time
	Elapsed       time.Duration
	StatusCode    int
	ErrorType     APIErrorType
	RetryAfter    time.Duration
	SleptBefore   time.Duration
}

type RetriesExhaustedReason string

const (
	ExhaustedAttemptBudget     RetriesExhaustedReason = "AttemptBudgetExhausted"
	ExhaustedDeadline          RetriesExhaustedReason = "DeadlineExhausted"
	ExhaustedRetryAfterTooLong RetriesExhaustedReason = "RetryAfterExceedsDeadline"
	ExhaustedNotRetryable      RetriesExhaustedReason = "NotRetryable"
	ExhaustedOperationExcluded RetriesExhaustedReason = "OperationExcluded"
	ExhaustedBodyNotReplayable RetriesExhaustedReason = "BodyNotReplayable"
	ExhaustedCancelled         RetriesExhaustedReason = "Cancelled"
)

type ResponseMeta struct {
	StatusCode              int
	RequestID               string
	RateLimit               RateLimitSnapshot
	RetryAfter              time.Duration
	IdempotentReplayed      bool
	IdempotencyKey          string
	Attempts                int
	Elapsed                 time.Duration
	AttemptRecords          []AttemptRecord
	RetriesExhaustedReason  RetriesExhaustedReason
	TransportMayHaveRetried bool
}

func headerInt(header http.Header, name string) (int, bool) {
	raw := header.Get(name)
	if raw == "" {
		return 0, false
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}

	return value, true
}

func rateLimitFrom(header http.Header) RateLimitSnapshot {
	snapshot := RateLimitSnapshot{}

	limit, hasLimit := headerInt(header, "RateLimit-Limit")
	remaining, hasRemaining := headerInt(header, "RateLimit-Remaining")
	reset, hasReset := headerInt(header, "RateLimit-Reset")
	policy := header.Get("RateLimit-Policy")
	scope := header.Get("RateLimit-Scope")

	if !hasLimit && !hasRemaining && !hasReset && policy == "" && scope == "" {
		return snapshot
	}

	snapshot.Present = true
	snapshot.Limit = limit
	snapshot.Remaining = remaining
	snapshot.ResetAfter = time.Duration(reset) * time.Second
	snapshot.Scope = scope
	snapshot.RawPolicy = policy

	if policy != "" {
		burst, window, ok := parsePolicy(policy)
		if ok {
			snapshot.PolicyBurst = burst
			snapshot.PolicyWindow = window
		}
	}

	return snapshot
}

func parsePolicy(policy string) (int, time.Duration, bool) {
	parts := strings.SplitN(policy, ";", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}

	burst, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}

	windowPart := strings.TrimSpace(parts[1])
	if !strings.HasPrefix(windowPart, "w=") {
		return 0, 0, false
	}

	seconds, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(windowPart, "w=")))
	if err != nil {
		return 0, 0, false
	}

	return burst, time.Duration(seconds) * time.Second, true
}

func retryAfterFrom(header http.Header) time.Duration {
	seconds, ok := headerInt(header, "Retry-After")
	if !ok {
		return 0
	}

	return time.Duration(seconds) * time.Second
}
