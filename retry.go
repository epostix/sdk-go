package epostix

import (
	"errors"
	"math"
	"time"
)

type RetryClass string

const (
	RetryClassSafeRead              RetryClass = "SafeRead"
	RetryClassNaturallyIdempotent   RetryClass = "NaturallyIdempotent"
	RetryClassDeduplicatedAdmission RetryClass = "DeduplicatedAdmission"
	RetryClassExcludedMutation      RetryClass = "ExcludedMutation"
	RetryClassStreaming             RetryClass = "Streaming"
)

type MutationRetryMode string

const (
	MutationRetryNever                MutationRetryMode = "Never"
	MutationRetryOnlyWhenDeduplicated MutationRetryMode = "OnlyWhenDeduplicated"
	MutationRetryAlways               MutationRetryMode = "Always"
)

type RetryPolicy struct {
	MaximumRetries    int
	OperationDeadline time.Duration
	ConnectTimeout    time.Duration
	ReadTimeout       time.Duration
	BackoffBase       time.Duration
	BackoffMaximum    time.Duration
	BackoffMultiplier float64
	HonorRetryAfter   bool
	MutationRetry     MutationRetryMode
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaximumRetries:    2,
		OperationDeadline: 30 * time.Second,
		ConnectTimeout:    5 * time.Second,
		ReadTimeout:       30 * time.Second,
		BackoffBase:       250 * time.Millisecond,
		BackoffMaximum:    2 * time.Second,
		BackoffMultiplier: 2,
		HonorRetryAfter:   true,
		MutationRetry:     MutationRetryOnlyWhenDeduplicated,
	}
}

func DisabledRetryPolicy() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.MaximumRetries = 0

	return policy
}

func UploadRetryPolicy() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.OperationDeadline = 180 * time.Second
	policy.ConnectTimeout = 30 * time.Second
	policy.ReadTimeout = 180 * time.Second

	return policy
}

func BatchRetryPolicy() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.OperationDeadline = 90 * time.Second
	policy.ReadTimeout = 90 * time.Second

	return policy
}

var neverRetry = map[APIErrorType]bool{
	APIErrorTypeAuthenticationFailed:  true,
	APIErrorTypeAPIKeyExpired:         true,
	APIErrorTypeInsufficientScope:     true,
	APIErrorTypeAPIKeyIPRestricted:    true,
	APIErrorTypeDomainScopeRestricted: true,
	APIErrorTypeTestModeRestricted:    true,
	APIErrorTypeWorkspaceSuspended:    true,
	APIErrorTypeValidationError:       true,
	APIErrorTypeInvalidRequest:        true,
	APIErrorTypeInvalidFromAddress:    true,
	APIErrorTypeInvalidSchedule:       true,
	APIErrorTypeBatchTooLarge:         true,
	APIErrorTypePayloadTooLarge:       true,
	APIErrorTypeUnsupportedMediaType:  true,
	APIErrorTypeMethodNotAllowed:      true,
	APIErrorTypeEmailNotCancellable:   true,
	APIErrorTypeDuplicateDetected:     true,
	APIErrorTypeDomainNotVerified:     true,
	APIErrorTypeAPIKeyInUse:           true,
	APIErrorTypeWebhookLimitReached:   true,
	APIErrorTypeWebhookURLNotAllowed:  true,
	APIErrorTypeNotFound:              true,
	APIErrorTypeDomainNotFound:        true,
	APIErrorTypeTemplateNotFound:      true,
	APIErrorTypeAttachmentNotFound:    true,
	APIErrorTypeAttachmentExpired:     true,
	APIErrorTypeAttachmentTooLarge:    true,
	APIErrorTypeTemplateRenderFailed:  true,
	APIErrorTypeIdempotencyConflict:   true,
	APIErrorTypeDailyQuotaExceeded:    true,
	APIErrorTypeMonthlyQuotaExceeded:  true,
}

type retryAction int

const (
	actionStop retryAction = iota
	actionRetry
)

type retryDecision struct {
	action retryAction
	delay  time.Duration
	reason RetriesExhaustedReason
}

type classifyContext struct {
	policy            RetryPolicy
	retryClass        RetryClass
	attemptNumber     int
	remaining         time.Duration
	err               error
	hasIdempotencyKey bool
	bodyIsReplayable  bool
	bytesWereWritten  bool
	responseStarted   bool
	random            func() float64
}

func fullJitter(policy RetryPolicy, attemptNumber int, random func() float64) time.Duration {
	ceiling := float64(policy.BackoffBase) * math.Pow(policy.BackoffMultiplier, float64(attemptNumber-1))
	if ceiling > float64(policy.BackoffMaximum) {
		ceiling = float64(policy.BackoffMaximum)
	}

	return time.Duration(random() * ceiling)
}

func fitsDeadline(delay time.Duration, ctx classifyContext) bool {
	return delay+ctx.policy.ConnectTimeout <= ctx.remaining
}

func classForTransient(ctx classifyContext) retryDecision {
	delay := fullJitter(ctx.policy, ctx.attemptNumber, ctx.random)

	switch ctx.retryClass {
	case RetryClassSafeRead, RetryClassNaturallyIdempotent:
		return retryDecision{action: actionRetry, delay: delay}

	case RetryClassStreaming:
		if ctx.responseStarted {
			return retryDecision{action: actionStop, reason: ExhaustedNotRetryable}
		}

		return retryDecision{action: actionRetry, delay: delay}

	case RetryClassExcludedMutation:
		if ctx.bytesWereWritten {
			return retryDecision{action: actionStop, reason: ExhaustedOperationExcluded}
		}

		return retryDecision{action: actionRetry, delay: delay}

	case RetryClassDeduplicatedAdmission:
		if ctx.policy.MutationRetry == MutationRetryNever {
			return retryDecision{action: actionStop, reason: ExhaustedOperationExcluded}
		}

		if ctx.policy.MutationRetry == MutationRetryAlways {
			return retryDecision{action: actionRetry, delay: delay}
		}

		if !ctx.hasIdempotencyKey {
			return retryDecision{action: actionStop, reason: ExhaustedOperationExcluded}
		}

		if !ctx.bodyIsReplayable {
			return retryDecision{action: actionStop, reason: ExhaustedBodyNotReplayable}
		}

		return retryDecision{action: actionRetry, delay: delay}
	}

	return retryDecision{action: actionStop, reason: ExhaustedNotRetryable}
}

func classifyAttempt(ctx classifyContext) retryDecision {
	if ctx.attemptNumber > ctx.policy.MaximumRetries {
		return retryDecision{action: actionStop, reason: ExhaustedAttemptBudget}
	}

	if ctx.remaining <= ctx.policy.ConnectTimeout {
		return retryDecision{action: actionStop, reason: ExhaustedDeadline}
	}

	var apiErr *APIError
	if errors.As(ctx.err, &apiErr) {
		if neverRetry[apiErr.Type] {
			return retryDecision{action: actionStop, reason: ExhaustedNotRetryable}
		}

		if apiErr.Kind == KindRateLimit || apiErr.Kind == KindIdempotencyInProgress {
			if !ctx.policy.HonorRetryAfter || apiErr.RetryAfter == 0 {
				return guardDeadline(classForTransient(ctx), ctx)
			}

			if !fitsDeadline(apiErr.RetryAfter, ctx) {
				return retryDecision{action: actionStop, reason: ExhaustedRetryAfterTooLong}
			}

			return retryDecision{action: actionRetry, delay: apiErr.RetryAfter}
		}

		retryableStatus := apiErr.Status == 408 || apiErr.Status >= 500
		retryableType := apiErr.Type == APIErrorTypeAttachmentFetchFailed

		if !retryableStatus && !retryableType {
			return retryDecision{action: actionStop, reason: ExhaustedNotRetryable}
		}

		return guardDeadline(classForTransient(ctx), ctx)
	}

	return guardDeadline(classForTransient(ctx), ctx)
}

func guardDeadline(decision retryDecision, ctx classifyContext) retryDecision {
	if decision.action == actionRetry && !fitsDeadline(decision.delay, ctx) {
		return retryDecision{action: actionStop, reason: ExhaustedDeadline}
	}

	return decision
}
