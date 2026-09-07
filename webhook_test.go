package epostix

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

type webhookFixture struct {
	Now              int64 `json:"now"`
	ToleranceSeconds int   `json:"toleranceSeconds"`
	Cases            []struct {
		Name            string   `json:"name"`
		Payload         string   `json:"payload"`
		Secrets         []string `json:"secrets"`
		Ts              int64    `json:"ts"`
		Signature       *string  `json:"signature"`
		DeliveryID      string   `json:"deliveryId"`
		HeaderTimestamp *int64   `json:"headerTimestamp"`
		Expect          struct {
			Valid              bool   `json:"valid"`
			Reason             string `json:"reason"`
			EventType          string `json:"eventType"`
			EventID            string `json:"eventId"`
			DeliveryID         string `json:"deliveryId"`
			Variant            string `json:"variant"`
			MatchedSecretIndex *int   `json:"matchedSecretIndex"`
		} `json:"expect"`
	} `json:"cases"`
}

func loadWebhookFixture(t *testing.T) webhookFixture {
	t.Helper()

	raw, err := os.ReadFile("testdata/webhook.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture webhookFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	return fixture
}

func TestWebhookVerificationVectors(t *testing.T) {
	fixture := loadWebhookFixture(t)

	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			verifier := NewWebhookVerifier(testCase.Secrets,
				WithTolerance(time.Duration(fixture.ToleranceSeconds)*time.Second),
				WithVerifierClock(func() time.Time { return time.Unix(fixture.Now, 0) }),
			)

			header := http.Header{}

			stamp := testCase.Ts
			if testCase.HeaderTimestamp != nil {
				stamp = *testCase.HeaderTimestamp
			}

			header.Set(WebhookHeaderTimestamp, strconv.FormatInt(stamp, 10))

			deliveryID := testCase.DeliveryID
			if deliveryID == "" {
				deliveryID = "whl_1"
			}

			header.Set(WebhookHeaderID, deliveryID)

			if testCase.Signature != nil {
				header.Set(WebhookHeaderSignature, *testCase.Signature)
			}

			event, delivery, err := verifier.VerifyAndDecode([]byte(testCase.Payload), header)

			if !testCase.Expect.Valid {
				var verifyErr *WebhookVerificationError
				if !errors.As(err, &verifyErr) {
					t.Fatalf("expected a verification error, got %v", err)
				}

				if string(verifyErr.Reason) != testCase.Expect.Reason {
					t.Fatalf("reason = %s, want %s", verifyErr.Reason, testCase.Expect.Reason)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if testCase.Expect.EventType != "" && string(event.Type) != testCase.Expect.EventType {
				t.Errorf("type = %s, want %s", event.Type, testCase.Expect.EventType)
			}

			if testCase.Expect.EventID != "" && event.ID != testCase.Expect.EventID {
				t.Errorf("id = %s, want %s", event.ID, testCase.Expect.EventID)
			}

			if testCase.Expect.DeliveryID != "" && delivery.DeliveryID != testCase.Expect.DeliveryID {
				t.Errorf("deliveryID = %s, want %s", delivery.DeliveryID, testCase.Expect.DeliveryID)
			}

			if testCase.Expect.MatchedSecretIndex != nil &&
				delivery.MatchedSecretIndex != *testCase.Expect.MatchedSecretIndex {
				t.Errorf("matchedSecretIndex = %d, want %d",
					delivery.MatchedSecretIndex, *testCase.Expect.MatchedSecretIndex)
			}

			switch testCase.Expect.Variant {
			case "EmailDeliveryEvent":
				if _, ok := event.EmailDelivery(); !ok {
					t.Error("expected an email delivery event")
				}
			case "InboundEmailEvent":
				data, ok := event.InboundEmail()
				if !ok || data.InboundID == "" {
					t.Error("expected an inbound event carrying inbound_id")
				}
			case "WebhookTestEvent":
				if _, ok := event.Test(); !ok {
					t.Error("expected a webhook test event")
				}
			case "UnknownWebhookEvent":
				if event.IsKnownType() {
					t.Error("expected an unknown event type")
				}
			}
		})
	}
}

func TestSignedEventIDIsTheDeduplicationKey(t *testing.T) {
	fixture := loadWebhookFixture(t)

	for _, testCase := range fixture.Cases {
		if testCase.Name != "delivery_id_is_not_signed_and_not_the_dedup_key" {
			continue
		}

		verifier := NewWebhookVerifier(testCase.Secrets,
			WithVerifierClock(func() time.Time { return time.Unix(fixture.Now, 0) }))

		header := http.Header{}
		header.Set(WebhookHeaderTimestamp, strconv.FormatInt(testCase.Ts, 10))
		header.Set(WebhookHeaderSignature, *testCase.Signature)
		header.Set(WebhookHeaderID, "whl_999")

		event, delivery, err := verifier.VerifyAndDecode([]byte(testCase.Payload), header)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if event.ID == delivery.DeliveryID {
			t.Fatal("the signed event id must differ from the unsigned delivery id")
		}

		if event.ID != "evt_1" {
			t.Fatalf("event id = %s, want evt_1", event.ID)
		}
	}
}
