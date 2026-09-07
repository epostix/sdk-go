package epostix

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	WebhookHeaderID        = "Webhook-Id"
	WebhookHeaderTimestamp = "Webhook-Timestamp"
	WebhookHeaderSignature = "Webhook-Signature"

	DefaultWebhookTolerance = 5 * time.Minute
)

type WebhookDelivery struct {
	DeliveryID         string
	Timestamp          time.Time
	MatchedSecretIndex int
	SignatureCount     int
}

type WebhookEvent struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

type EmailDeliveryData struct {
	EmailID string `json:"email_id"`
}

type InboundEmailData struct {
	InboundID string `json:"inbound_id"`
}

type WebhookTestData struct {
	Message string `json:"message"`
}

var deliveryEventTypes = map[EventType]bool{
	EventTypeEmailSent:       true,
	EventTypeEmailDelivered:  true,
	EventTypeEmailBounced:    true,
	EventTypeEmailOpened:     true,
	EventTypeEmailClicked:    true,
	EventTypeEmailComplained: true,
	EventTypeEmailFailed:     true,
	EventTypeEmailDelayed:    true,
}

func (e *WebhookEvent) EmailDelivery() (*EmailDeliveryData, bool) {
	if !deliveryEventTypes[e.Type] {
		return nil, false
	}

	var data EmailDeliveryData
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, false
	}

	return &data, true
}

func (e *WebhookEvent) InboundEmail() (*InboundEmailData, bool) {
	if e.Type != EventTypeEmailReceived {
		return nil, false
	}

	var data InboundEmailData
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, false
	}

	return &data, true
}

func (e *WebhookEvent) Test() (*WebhookTestData, bool) {
	if e.Type != EventTypeWebhookTest {
		return nil, false
	}

	var data WebhookTestData
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, false
	}

	return &data, true
}

func (e *WebhookEvent) IsKnownType() bool {
	return e.Type.Known()
}

type WebhookVerifier struct {
	secrets   []string
	tolerance time.Duration
	now       func() time.Time
}

type VerifierOption func(*WebhookVerifier)

func WithTolerance(tolerance time.Duration) VerifierOption {
	return func(v *WebhookVerifier) {
		v.tolerance = tolerance
	}
}

func WithVerifierClock(now func() time.Time) VerifierOption {
	return func(v *WebhookVerifier) {
		if now != nil {
			v.now = now
		}
	}
}

func NewWebhookVerifier(secrets []string, opts ...VerifierOption) *WebhookVerifier {
	verifier := &WebhookVerifier{
		secrets:   append([]string(nil), secrets...),
		tolerance: DefaultWebhookTolerance,
		now:       time.Now,
	}

	for _, opt := range opts {
		opt(verifier)
	}

	return verifier
}

func NewWebhookVerifierFromRotation(current, previous string) *WebhookVerifier {
	return NewWebhookVerifier([]string{current, previous})
}

func Sign(payload []byte, secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(payload)

	return hex.EncodeToString(mac.Sum(nil))
}

func parseSignatureHeader(raw string) (int64, []string, bool) {
	var (
		timestamp  int64
		haveTime   bool
		signatures []string
	)

	for _, part := range strings.Split(raw, ",") {
		separator := strings.Index(part, "=")
		if separator < 0 {
			return 0, nil, false
		}

		key := strings.TrimSpace(part[:separator])
		value := strings.TrimSpace(part[separator+1:])

		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, nil, false
			}

			timestamp = parsed
			haveTime = true
		case "v1":
			signatures = append(signatures, value)
		}
	}

	if !haveTime || len(signatures) == 0 {
		return 0, nil, false
	}

	return timestamp, signatures, true
}

func (v *WebhookVerifier) Verify(payload []byte, header http.Header) (*WebhookDelivery, error) {
	signatureHeader := header.Get(WebhookHeaderSignature)
	if signatureHeader == "" {
		return nil, &WebhookVerificationError{
			Reason: ReasonMissingSignatureHeader, Message: WebhookHeaderSignature + " is missing",
		}
	}

	timestampHeader := header.Get(WebhookHeaderTimestamp)
	if timestampHeader == "" {
		return nil, &WebhookVerificationError{
			Reason: ReasonMissingTimestampHeader, Message: WebhookHeaderTimestamp + " is missing",
		}
	}

	timestamp, signatures, ok := parseSignatureHeader(signatureHeader)
	if !ok {
		return nil, &WebhookVerificationError{
			Reason:  ReasonMalformedSignatureHeader,
			Message: WebhookHeaderSignature + " is not in the form t=...,v1=...",
		}
	}

	if strconv.FormatInt(timestamp, 10) != strings.TrimSpace(timestampHeader) {
		return nil, &WebhookVerificationError{
			Reason:  ReasonMalformedSignatureHeader,
			Message: WebhookHeaderTimestamp + " and the signed timestamp disagree",
		}
	}

	age := v.now().Unix() - timestamp
	if age < 0 {
		age = -age
	}

	if time.Duration(age)*time.Second > v.tolerance {
		return nil, &WebhookVerificationError{
			Reason:  ReasonTimestampOutOfTolerance,
			Message: fmt.Sprintf("the signature timestamp is outside the %s tolerance", v.tolerance),
		}
	}

	matched := -1

	for index, secret := range v.secrets {
		expected := Sign(payload, secret, timestamp)

		for _, candidate := range signatures {
			if len(expected) == len(candidate) &&
				subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1 &&
				matched < 0 {
				matched = index
			}
		}
	}

	if matched < 0 {
		return nil, &WebhookVerificationError{
			Reason: ReasonNoMatchingSignature, Message: "no configured secret produced a matching signature",
		}
	}

	return &WebhookDelivery{
		DeliveryID:         header.Get(WebhookHeaderID),
		Timestamp:          time.Unix(timestamp, 0).UTC(),
		MatchedSecretIndex: matched,
		SignatureCount:     len(signatures),
	}, nil
}

func (v *WebhookVerifier) VerifyAndDecode(payload []byte, header http.Header) (*WebhookEvent, *WebhookDelivery, error) {
	delivery, err := v.Verify(payload, header)
	if err != nil {
		return nil, nil, err
	}

	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, nil, &WebhookVerificationError{
			Reason:  ReasonMalformedPayload,
			Message: "the signature verified but the payload is not valid JSON",
		}
	}

	return &event, delivery, nil
}

func DecodeWebhookWithoutVerifying(payload []byte) (*WebhookEvent, error) {
	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("epostix: decode webhook event: %w", err)
	}

	return &event, nil
}
