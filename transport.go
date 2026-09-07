package epostix

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	IdempotencyKeyMaximumLength = 255
	IdempotencyRetention        = 24 * time.Hour
	rawBodySnippetLimit         = 8192
)

type operationSpec struct {
	ID            string
	Method        string
	PathLiterals  []string
	PathParams    []string
	SuccessStatus int
	Empty         bool
	Binary        bool
	Idempotent    bool
	RetryClass    RetryClass
}

type requestInput struct {
	op    operationSpec
	path  []string
	query url.Values
	body  any
}

type paginateInput struct {
	op          operationSpec
	path        []string
	query       url.Values
	cursorParam string
}

type requestConfig struct {
	idempotencyKey    string
	suppressKey       bool
	retryPolicy       *RetryPolicy
	headers           map[string]string
	operationDeadline time.Duration
}

type RequestOption func(*requestConfig)

func WithIdempotencyKey(key string) RequestOption {
	return func(c *requestConfig) {
		c.idempotencyKey = key
	}
}

func WithoutIdempotencyKey() RequestOption {
	return func(c *requestConfig) {
		c.suppressKey = true
	}
}

func WithRequestRetryPolicy(policy RetryPolicy) RequestOption {
	return func(c *requestConfig) {
		c.retryPolicy = &policy
	}
}

func WithRequestHeader(name, value string) RequestOption {
	return func(c *requestConfig) {
		if c.headers == nil {
			c.headers = map[string]string{}
		}
		c.headers[name] = value
	}
}

func WithOperationDeadline(deadline time.Duration) RequestOption {
	return func(c *requestConfig) {
		c.operationDeadline = deadline
	}
}

func formatQueryValue(value any) string {
	switch typed := value.(type) {
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case int:
		return strconv.Itoa(typed)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func buildPath(spec operationSpec, values []string) (string, error) {
	var builder strings.Builder

	for index, literal := range spec.PathLiterals {
		builder.WriteString(literal)

		if index < len(spec.PathParams) {
			if index >= len(values) || values[index] == "" {
				return "", &ConfigError{
					Message: fmt.Sprintf("%s: path parameter %q is required", spec.ID, spec.PathParams[index]),
				}
			}

			builder.WriteString(url.PathEscape(values[index]))
		}
	}

	return builder.String(), nil
}

func generateIdempotencyKey() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("epostix: generate idempotency key: %w", err)
	}

	return "epx_auto_" + hex.EncodeToString(buffer), nil
}

func execute[T any](ctx context.Context, client *Client, input *requestInput, opts ...RequestOption) (*T, error) {
	if client.configErr != nil {
		return nil, client.configErr
	}

	config := requestConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	policy := client.retryPolicy
	if config.retryPolicy != nil {
		policy = *config.retryPolicy
	}

	deadline := policy.OperationDeadline
	if config.operationDeadline > 0 {
		deadline = config.operationDeadline
	}

	key, err := resolveIdempotencyKey(client, input.op, config)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := serializeBody(input.body)
	if err != nil {
		return nil, err
	}

	path, err := buildPath(input.op, input.path)
	if err != nil {
		return nil, err
	}

	endpoint := client.baseURL + path
	if len(input.query) > 0 {
		endpoint += "?" + input.query.Encode()
	}

	started := client.now()
	records := make([]AttemptRecord, 0, policy.MaximumRetries+1)

	var (
		lastErr          error
		bytesWereWritten bool
		sleptBefore      time.Duration
		attemptNumber    int
		exhausted        RetriesExhaustedReason
	)

	for {
		attemptNumber++

		if err := ctx.Err(); err != nil {
			return nil, wrapContextError(err)
		}

		attemptStart := client.now()
		remaining := deadline - attemptStart.Sub(started)

		attemptCtx, cancel := context.WithTimeout(ctx, minDuration(remaining, policy.ReadTimeout))

		response, requestErr := client.doAttempt(attemptCtx, input.op, endpoint, bodyBytes, key, config)
		if bodyBytes != nil || requestErr == nil {
			bytesWereWritten = true
		}

		elapsed := client.now().Sub(attemptStart)

		if requestErr == nil {
			meta := metaFrom(response, attemptNumber, client.now().Sub(started), key)

			if response.StatusCode >= 200 && response.StatusCode < 300 {
				records = append(records, AttemptRecord{
					AttemptNumber: attemptNumber, StartedAt: attemptStart, Elapsed: elapsed,
					StatusCode: response.StatusCode, SleptBefore: sleptBefore,
				})
				meta.AttemptRecords = records
				meta.Attempts = attemptNumber

				result, decodeErr := decodeSuccess[T](input.op, response, meta)
				response.Body.Close()
				cancel()

				return result, decodeErr
			}

			apiErr := errorFrom(response, meta)
			response.Body.Close()
			cancel()

			lastErr = apiErr
			records = append(records, AttemptRecord{
				AttemptNumber: attemptNumber, StartedAt: attemptStart, Elapsed: elapsed,
				StatusCode: response.StatusCode, ErrorType: apiErr.Type,
				RetryAfter: apiErr.RetryAfter, SleptBefore: sleptBefore,
			})
		} else {
			cancel()

			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, wrapContextError(ctxErr)
			}

			lastErr = &TransportError{Message: "the request could not be completed", Cause: requestErr}
			records = append(records, AttemptRecord{
				AttemptNumber: attemptNumber, StartedAt: attemptStart, Elapsed: elapsed,
				SleptBefore: sleptBefore,
			})
		}

		decision := classifyAttempt(classifyContext{
			policy:            policy,
			retryClass:        input.op.RetryClass,
			attemptNumber:     attemptNumber,
			remaining:         deadline - client.now().Sub(started),
			err:               lastErr,
			hasIdempotencyKey: key != "",
			bodyIsReplayable:  true,
			bytesWereWritten:  bytesWereWritten,
			random:            client.random,
		})

		if decision.action == actionStop {
			exhausted = decision.reason
			break
		}

		sleptBefore = decision.delay

		timer := time.NewTimer(decision.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, wrapContextError(ctx.Err())
		case <-timer.C:
		}
	}

	return nil, finalError(input.op, lastErr, exhausted, key, bytesWereWritten, attemptNumber,
		client.now().Sub(started), records)
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return time.Millisecond
	}

	if a < b {
		return a
	}

	return b
}

func wrapContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Phase: PhaseOperationDeadline, CallerDeadline: true, Cause: err}
	}

	return err
}

func finalError(
	spec operationSpec,
	lastErr error,
	exhausted RetriesExhaustedReason,
	key string,
	bytesWereWritten bool,
	attempts int,
	elapsed time.Duration,
	records []AttemptRecord,
) error {
	var apiErr *APIError
	if errors.As(lastErr, &apiErr) {
		apiErr.Meta.RetriesExhaustedReason = exhausted
		apiErr.Meta.Attempts = attempts
		apiErr.Meta.AttemptRecords = records

		return apiErr
	}

	if spec.Method != http.MethodGet && bytesWereWritten {
		return &OutcomeUnknownError{
			Operation:         spec.ID,
			IdempotencyKey:    key,
			ReplayableWithKey: key != "" && spec.RetryClass == RetryClassDeduplicatedAdmission,
			Attempts:          attempts,
			Meta: ResponseMeta{
				Attempts: attempts, Elapsed: elapsed, AttemptRecords: records,
				IdempotencyKey: key, RetriesExhaustedReason: exhausted,
			},
			Cause: lastErr,
		}
	}

	return lastErr
}

func resolveIdempotencyKey(client *Client, spec operationSpec, config requestConfig) (string, error) {
	if config.idempotencyKey != "" {
		if !spec.Idempotent {
			return "", &ConfigError{
				Message: fmt.Sprintf(
					"%s does not support an idempotency key: the server does not deduplicate this operation",
					spec.ID),
			}
		}

		if len(config.idempotencyKey) > IdempotencyKeyMaximumLength {
			return "", &ConfigError{
				Message: fmt.Sprintf("idempotency key must be %d characters or fewer", IdempotencyKeyMaximumLength),
			}
		}

		return config.idempotencyKey, nil
	}

	if config.suppressKey || !spec.Idempotent || !client.automaticKeys {
		return "", nil
	}

	return generateIdempotencyKey()
}

func serializeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("epostix: encode request body: %w", err)
	}

	return encoded, nil
}

func (c *Client) doAttempt(
	ctx context.Context,
	spec operationSpec,
	endpoint string,
	body []byte,
	key string,
	config requestConfig,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(ctx, spec.Method, endpoint, reader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("User-Agent", c.userAgent)

	for name, value := range c.defaultHeaders {
		request.Header.Set(name, value)
	}

	for name, value := range config.headers {
		request.Header.Set(name, value)
	}

	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}

	return c.httpClient.Do(request)
}

func metaFrom(response *http.Response, attempt int, elapsed time.Duration, key string) ResponseMeta {
	requestID := response.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = response.Header.Get("Request-Id")
	}

	return ResponseMeta{
		StatusCode:         response.StatusCode,
		RequestID:          requestID,
		RateLimit:          rateLimitFrom(response.Header),
		RetryAfter:         retryAfterFrom(response.Header),
		IdempotentReplayed: response.Header.Get("Idempotent-Replayed") == "true",
		IdempotencyKey:     key,
		Attempts:           attempt,
		Elapsed:            elapsed,
	}
}

func decodeSuccess[T any](spec operationSpec, response *http.Response, meta ResponseMeta) (*T, error) {
	result := new(T)

	if spec.Empty {
		io.Copy(io.Discard, response.Body)
		attachMeta(result, meta)

		return result, nil
	}

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, &TransportError{Message: "the response body could not be read", Cause: err, Meta: meta}
	}

	if len(payload) > 0 {
		if err := json.Unmarshal(payload, result); err != nil {
			return nil, &TransportError{Message: "the response body could not be decoded", Cause: err, Meta: meta}
		}
	}

	attachMeta(result, meta)

	return result, nil
}

type metaCarrier interface {
	setMeta(ResponseMeta)
}

func attachMeta(value any, meta ResponseMeta) {
	if carrier, ok := value.(metaCarrier); ok {
		carrier.setMeta(meta)
	}
}

type wireError struct {
	Status    int           `json:"status"`
	Type      string        `json:"type"`
	Message   string        `json:"message"`
	RequestID string        `json:"request_id"`
	DocURL    string        `json:"doc_url"`
	Details   []ErrorDetail `json:"details"`
}

func errorFrom(response *http.Response, meta ResponseMeta) *APIError {
	payload, _ := io.ReadAll(response.Body)

	var wire wireError
	decodeErr := json.Unmarshal(payload, &wire)

	if decodeErr != nil || wire.Type == "" {
		snippet := payload
		truncated := false

		if len(snippet) > rawBodySnippetLimit {
			snippet = snippet[:rawBodySnippetLimit]
			truncated = true
		}

		return &APIError{
			Kind:             kindForStatus(response.StatusCode),
			Status:           response.StatusCode,
			APIMessage:       http.StatusText(response.StatusCode),
			RequestID:        meta.RequestID,
			RetryAfter:       meta.RetryAfter,
			Meta:             meta,
			RawBodySnippet:   string(snippet),
			RawBodyTruncated: truncated,
		}
	}

	errType := APIErrorType(wire.Type)
	kind, known := errorKindByType[errType]

	if !known {
		kind = kindForStatus(response.StatusCode)
	}

	status := wire.Status
	if status == 0 {
		status = response.StatusCode
	}

	quota := QuotaPeriodNone
	switch errType {
	case APIErrorTypeDailyQuotaExceeded:
		quota = QuotaPeriodDaily
	case APIErrorTypeMonthlyQuotaExceeded:
		quota = QuotaPeriodMonthly
	}

	retryAfter := meta.RetryAfter
	if quota == QuotaPeriodMonthly {
		retryAfter = 0
	}

	requestID := wire.RequestID
	if requestID == "" {
		requestID = meta.RequestID
	}

	return &APIError{
		Kind:        kind,
		Type:        errType,
		KnownType:   known,
		Status:      status,
		APIMessage:  wire.Message,
		RequestID:   requestID,
		DocURL:      wire.DocURL,
		Details:     wire.Details,
		RetryAfter:  retryAfter,
		QuotaPeriod: quota,
		Meta:        meta,
	}
}
