package epostix

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type RawContent struct {
	Body        io.ReadCloser
	ContentType string
	Meta        ResponseMeta
}

func (r *InboundResource) GetInboundEmailRaw(
	ctx context.Context,
	inboundID string,
	opts ...RequestOption,
) (*RawContent, error) {
	if r.client.configErr != nil {
		return nil, r.client.configErr
	}

	config := requestConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	spec := operations.GetInboundEmailRaw

	path, err := buildPath(spec, []string{inboundID})
	if err != nil {
		return nil, err
	}

	response, err := r.client.doAttempt(ctx, spec, r.client.baseURL+path, nil, "", config)
	if err != nil {
		return nil, &TransportError{Message: "the raw message could not be fetched", Cause: err}
	}

	meta := metaFrom(response, 1, 0, "")

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := errorFrom(response, meta)
		response.Body.Close()

		return nil, apiErr
	}

	return &RawContent{
		Body:        response.Body,
		ContentType: response.Header.Get("Content-Type"),
		Meta:        meta,
	}, nil
}

func (r *InboundResource) GetInboundEmailRawBytes(
	ctx context.Context,
	inboundID string,
	maximumBytes int64,
	opts ...RequestOption,
) ([]byte, error) {
	if maximumBytes <= 0 {
		return nil, &ConfigError{Message: "GetInboundEmailRawBytes requires a positive maximumBytes"}
	}

	raw, err := r.GetInboundEmailRaw(ctx, inboundID, opts...)
	if err != nil {
		return nil, err
	}

	defer raw.Body.Close()

	content, err := io.ReadAll(io.LimitReader(raw.Body, maximumBytes+1))
	if err != nil {
		return nil, &TransportError{Message: "the raw message could not be read", Cause: err}
	}

	if int64(len(content)) > maximumBytes {
		return nil, &ConfigError{
			Message: fmt.Sprintf("the raw message is larger than the %d byte cap you set", maximumBytes),
		}
	}

	return content, nil
}

func (r *AttachmentsResource) UploadAttachment(
	ctx context.Context,
	filename string,
	content []byte,
	contentType string,
	opts ...RequestOption,
) (*AttachmentResponse, error) {
	if err := checkAttachmentSize(filename, len(content)); err != nil {
		return nil, err
	}

	body := AttachmentUpload{
		Filename:    filename,
		Content:     base64.StdEncoding.EncodeToString(content),
		ContentType: contentType,
	}

	return execute[AttachmentResponse](ctx, r.client, &requestInput{
		op:   operations.UploadAttachment,
		body: &body,
	}, opts...)
}

type RawRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
	Header http.Header
}

type RawResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	Meta       ResponseMeta
}

func (c *Client) Do(ctx context.Context, request *RawRequest, opts ...RequestOption) (*RawResponse, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}

	config := requestConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	endpoint := c.baseURL + request.Path
	if len(request.Query) > 0 {
		endpoint += "?" + request.Query.Encode()
	}

	spec := operationSpec{ID: "raw", Method: request.Method, RetryClass: RetryClassSafeRead}

	response, err := c.doAttempt(ctx, spec, endpoint, request.Body, config.idempotencyKey, config)
	if err != nil {
		return nil, &TransportError{Message: "the request could not be completed", Cause: err}
	}

	return &RawResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
		Meta:       metaFrom(response, 1, 0, config.idempotencyKey),
	}, nil
}
