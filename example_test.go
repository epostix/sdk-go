package epostix_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/epostix/sdk-go"
)

func Example() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	email, err := client.Emails.SendEmail(context.Background(), &epostix.EmailCreate{
		From:    epostix.Addr("billing@mailer.example.com"),
		To:      []string{"delivered@epostix.dev"},
		Subject: "Your receipt",
		Text:    ptr("Your order is confirmed."),
	}, epostix.WithIdempotencyKey("order-4821-receipt"))
	if err != nil {
		fmt.Println("send failed:", err)

		return
	}

	fmt.Println("accepted:", email.ID)
}

func ExampleClient_WithAPIKey() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	tenant := client.WithAPIKey(os.Getenv("TENANT_API_KEY"))

	fmt.Println(client.KeyEnvironment(), tenant.KeyEnvironment())
}

func ExampleAPIError() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	_, err := client.Emails.GetEmail(context.Background(), "eml_missing")

	var apiErr *epostix.APIError
	if errors.As(err, &apiErr) {
		fmt.Println(apiErr.Type, apiErr.Status, apiErr.RequestID)

		for _, detail := range apiErr.Details {
			fmt.Println(detail.Field, detail.Code, detail.Message)
		}
	}

	if errors.Is(err, epostix.ErrRateLimited) {
		fmt.Println("retry after", apiErr.RetryAfter)
	}
}

func ExampleIterator() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	status := epostix.ListEmailsStatusDelivered
	iterator := client.Emails.ListEmailsIterator(context.Background(), &epostix.ListEmailsParams{
		Status: &status,
	})

	for iterator.Next() {
		fmt.Println(iterator.Current().ID)
	}

	if err := iterator.Err(); err != nil {
		fmt.Println("iteration stopped:", err)
	}
}

func ExampleIterator_Collect() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	page, err := client.Emails.ListEmailsIterator(context.Background(), nil).Collect(500)
	if err != nil {
		fmt.Println("collect failed:", err)

		return
	}

	fmt.Println(len(page.Items), "collected, truncated:", page.Truncated)
}

func ExampleWebhookVerifier() {
	verifier := epostix.NewWebhookVerifier([]string{os.Getenv("EPOSTIX_WEBHOOK_SECRET")})

	handler := func(w http.ResponseWriter, r *http.Request) {
		payload, err := readAll(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		event, delivery, err := verifier.VerifyAndDecode(payload, r.Header)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		if data, ok := event.EmailDelivery(); ok {
			fmt.Println(event.ID, data.EmailID, delivery.DeliveryID)
		}

		w.WriteHeader(http.StatusOK)
	}

	_ = handler
}

func ExampleNewWebhookVerifierFromRotation() {
	verifier := epostix.NewWebhookVerifierFromRotation(
		os.Getenv("EPOSTIX_WEBHOOK_SECRET"),
		os.Getenv("EPOSTIX_WEBHOOK_SECRET_PREVIOUS"),
	)

	_ = verifier
}

func ExampleOptional() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))
	ctx := context.Background()

	client.Emails.UpdateEmail(ctx, "eml_1", &epostix.EmailUpdate{
		ScheduledAt: epostix.Set(time.Now().Add(2 * time.Hour)),
	})

	client.Emails.UpdateEmail(ctx, "eml_1", &epostix.EmailUpdate{
		ScheduledAt: epostix.Null[time.Time](),
	})

	client.Emails.UpdateEmail(ctx, "eml_1", &epostix.EmailUpdate{})
}

func ExampleAttachmentFromBytes() {
	receipt, err := epostix.AttachmentFromBytes("receipt.pdf", []byte("%PDF-1.7"), "application/pdf")
	if err != nil {
		fmt.Println("attachment rejected:", err)

		return
	}

	fmt.Println(receipt.Filename, receipt.ContentType)
	// Output: receipt.pdf application/pdf
}

func ExampleAttachmentFromRemoteURL() {
	remote := epostix.AttachmentFromRemoteURL("invoice.pdf", "https://example.com/invoice.pdf")

	fmt.Println(remote.Filename, remote.ContentURL)
	// Output: invoice.pdf https://example.com/invoice.pdf
}

func ExampleWithRetryPolicy() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"),
		epostix.WithRetryPolicy(epostix.DisabledRetryPolicy()),
		epostix.WithHTTPClient(&http.Client{Timeout: 0}),
	)

	_ = client
}

func ExampleOutcomeUnknownError() {
	client := epostix.New(os.Getenv("EPOSTIX_API_KEY"))

	_, err := client.Emails.SendEmail(context.Background(), &epostix.EmailCreate{
		From:    epostix.Addr("billing@mailer.example.com"),
		To:      []string{"delivered@epostix.dev"},
		Subject: "Your receipt",
	}, epostix.WithIdempotencyKey("order-4821-receipt"))

	var unknown *epostix.OutcomeUnknownError
	if errors.As(err, &unknown) && unknown.ReplayableWithKey {
		fmt.Println("reissue the identical call with key", unknown.IdempotencyKey)
	}
}

func ptr[T any](value T) *T {
	return &value
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()

	buffer := make([]byte, r.ContentLength)
	_, err := r.Body.Read(buffer)

	return buffer, err
}
