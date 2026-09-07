package epostix

import (
	"errors"
	"fmt"
	"time"
)

type ErrorKind string

const (
	KindInvalidRequest        ErrorKind = "invalid_request"
	KindValidation            ErrorKind = "validation"
	KindAuthentication        ErrorKind = "authentication"
	KindPermission            ErrorKind = "permission"
	KindSuspended             ErrorKind = "suspended"
	KindNotFound              ErrorKind = "not_found"
	KindConflict              ErrorKind = "conflict"
	KindIdempotencyConflict   ErrorKind = "idempotency_conflict"
	KindIdempotencyInProgress ErrorKind = "idempotency_in_progress"
	KindRateLimit             ErrorKind = "rate_limit"
	KindQuota                 ErrorKind = "quota"
	KindContent               ErrorKind = "content"
	KindServer                ErrorKind = "server"
	KindUnknown               ErrorKind = "unknown"
)

type QuotaPeriod string

const (
	QuotaPeriodNone    QuotaPeriod = ""
	QuotaPeriodDaily   QuotaPeriod = "daily"
	QuotaPeriodMonthly QuotaPeriod = "monthly"
)

var (
	ErrRateLimited           = errors.New("epostix: rate limited")
	ErrQuotaExceeded         = errors.New("epostix: quota exceeded")
	ErrIdempotencyConflict   = errors.New("epostix: idempotency conflict")
	ErrIdempotencyInProgress = errors.New("epostix: idempotent request in progress")
	ErrAuthentication        = errors.New("epostix: authentication failed")
	ErrPermission            = errors.New("epostix: not permitted")
	ErrNotFound              = errors.New("epostix: not found")
	ErrValidation            = errors.New("epostix: validation failed")
	ErrSuspended             = errors.New("epostix: workspace suspended")
	ErrServer                = errors.New("epostix: server error")
	ErrInvalidRequest        = errors.New("epostix: invalid request")
	ErrConflict              = errors.New("epostix: conflict")
	ErrContent               = errors.New("epostix: content rejected")
)

var kindSentinels = map[ErrorKind]error{
	KindInvalidRequest:        ErrInvalidRequest,
	KindValidation:            ErrValidation,
	KindAuthentication:        ErrAuthentication,
	KindPermission:            ErrPermission,
	KindSuspended:             ErrSuspended,
	KindNotFound:              ErrNotFound,
	KindConflict:              ErrConflict,
	KindIdempotencyConflict:   ErrIdempotencyConflict,
	KindIdempotencyInProgress: ErrIdempotencyInProgress,
	KindRateLimit:             ErrRateLimited,
	KindQuota:                 ErrQuotaExceeded,
	KindContent:               ErrContent,
	KindServer:                ErrServer,
}

type ErrorDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type APIError struct {
	Kind             ErrorKind
	Type             APIErrorType
	KnownType        bool
	Status           int
	APIMessage       string
	RequestID        string
	DocURL           string
	Details          []ErrorDetail
	RetryAfter       time.Duration
	QuotaPeriod      QuotaPeriod
	Meta             ResponseMeta
	RawBodySnippet   string
	RawBodyTruncated bool
}

func (e *APIError) Error() string {
	label := string(e.Type)
	if label == "" {
		label = fmt.Sprintf("http_%d", e.Status)
	}

	return fmt.Sprintf("epostix: %s: %s (status=%d, request_id=%s)",
		label, e.APIMessage, e.Status, e.RequestID)
}

func (e *APIError) Is(target error) bool {
	sentinel, ok := kindSentinels[e.Kind]
	if !ok {
		return false
	}

	return errors.Is(sentinel, target)
}

type TimeoutPhase string

const (
	PhaseConnect           TimeoutPhase = "connect"
	PhaseRead              TimeoutPhase = "read"
	PhaseOperationDeadline TimeoutPhase = "operation_deadline"
)

type TransportError struct {
	Message string
	Meta    ResponseMeta
	Cause   error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("epostix: %s: %v", e.Message, e.Cause)
}

func (e *TransportError) Unwrap() error {
	return e.Cause
}

type TimeoutError struct {
	Phase          TimeoutPhase
	CallerDeadline bool
	Meta           ResponseMeta
	Cause          error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("epostix: timed out during %s: %v", e.Phase, e.Cause)
}

func (e *TimeoutError) Unwrap() error {
	return e.Cause
}

type OutcomeUnknownError struct {
	Operation         string
	IdempotencyKey    string
	ReplayableWithKey bool
	Attempts          int
	Meta              ResponseMeta
	Cause             error
}

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf(
		"epostix: the outcome of %s could not be established: the request may or may not have been accepted: %v",
		e.Operation, e.Cause)
}

func (e *OutcomeUnknownError) Unwrap() error {
	return e.Cause
}

type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return "epostix: " + e.Message
}

type PaginationFailureReason string

const (
	ReasonMissingCursor           PaginationFailureReason = "MissingCursor"
	ReasonRepeatedCursor          PaginationFailureReason = "RepeatedCursor"
	ReasonContinuationUnsupported PaginationFailureReason = "ContinuationUnsupported"
	ReasonPageLimitExceeded       PaginationFailureReason = "PageLimitExceeded"
)

type PaginationError struct {
	Reason    PaginationFailureReason
	Operation string
	Message   string
}

func (e *PaginationError) Error() string {
	return fmt.Sprintf("epostix: %s: %s", e.Operation, e.Message)
}

type WebhookFailureReason string

const (
	ReasonMissingSignatureHeader   WebhookFailureReason = "MissingSignatureHeader"
	ReasonMissingTimestampHeader   WebhookFailureReason = "MissingTimestampHeader"
	ReasonMalformedSignatureHeader WebhookFailureReason = "MalformedSignatureHeader"
	ReasonTimestampOutOfTolerance  WebhookFailureReason = "TimestampOutOfTolerance"
	ReasonNoMatchingSignature      WebhookFailureReason = "NoMatchingSignature"
	ReasonMalformedPayload         WebhookFailureReason = "MalformedPayload"
)

type WebhookVerificationError struct {
	Reason  WebhookFailureReason
	Message string
}

func (e *WebhookVerificationError) Error() string {
	return "epostix: webhook verification failed: " + e.Message
}

var errorKindByType = map[APIErrorType]ErrorKind{
	APIErrorTypeInvalidRequest:        KindInvalidRequest,
	APIErrorTypeUnsupportedMediaType:  KindInvalidRequest,
	APIErrorTypePayloadTooLarge:       KindInvalidRequest,
	APIErrorTypeMethodNotAllowed:      KindInvalidRequest,
	APIErrorTypeValidationError:       KindValidation,
	APIErrorTypeInvalidFromAddress:    KindValidation,
	APIErrorTypeInvalidSchedule:       KindValidation,
	APIErrorTypeBatchTooLarge:         KindValidation,
	APIErrorTypeAuthenticationFailed:  KindAuthentication,
	APIErrorTypeAPIKeyExpired:         KindAuthentication,
	APIErrorTypeInsufficientScope:     KindPermission,
	APIErrorTypeAPIKeyIPRestricted:    KindPermission,
	APIErrorTypeDomainScopeRestricted: KindPermission,
	APIErrorTypeTestModeRestricted:    KindPermission,
	APIErrorTypeWorkspaceSuspended:    KindSuspended,
	APIErrorTypeNotFound:              KindNotFound,
	APIErrorTypeDomainNotFound:        KindNotFound,
	APIErrorTypeTemplateNotFound:      KindNotFound,
	APIErrorTypeAttachmentNotFound:    KindNotFound,
	APIErrorTypeDuplicateDetected:     KindConflict,
	APIErrorTypeEmailNotCancellable:   KindConflict,
	APIErrorTypeDomainNotVerified:     KindConflict,
	APIErrorTypeAPIKeyInUse:           KindConflict,
	APIErrorTypeWebhookLimitReached:   KindConflict,
	APIErrorTypeWebhookURLNotAllowed:  KindConflict,
	APIErrorTypeIdempotencyConflict:   KindIdempotencyConflict,
	APIErrorTypeIdempotencyInProgress: KindIdempotencyInProgress,
	APIErrorTypeRateLimitExceeded:     KindRateLimit,
	APIErrorTypeDailyQuotaExceeded:    KindQuota,
	APIErrorTypeMonthlyQuotaExceeded:  KindQuota,
	APIErrorTypeTemplateRenderFailed:  KindContent,
	APIErrorTypeAttachmentExpired:     KindContent,
	APIErrorTypeAttachmentTooLarge:    KindContent,
	APIErrorTypeAttachmentFetchFailed: KindContent,
	APIErrorTypeInternalError:         KindServer,
	APIErrorTypeServiceUnavailable:    KindServer,
}

func kindForStatus(status int) ErrorKind {
	switch {
	case status == 401:
		return KindAuthentication
	case status == 403:
		return KindPermission
	case status == 404:
		return KindNotFound
	case status == 409:
		return KindConflict
	case status == 429:
		return KindRateLimit
	case status >= 500:
		return KindServer
	case status >= 400:
		return KindInvalidRequest
	default:
		return KindUnknown
	}
}
