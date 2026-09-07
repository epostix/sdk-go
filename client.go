package epostix

import (
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const (
	ProductionBaseURL = "https://api.epostix.com/v1"
	StagingBaseURL    = "https://api.staging.epostix.com/v1"

	SandboxDomain = "epostix.dev"

	DeduplicationScope = "api_key + environment + method + path + key, 24h"
)

type KeyEnvironment string

const (
	KeyEnvironmentLive         KeyEnvironment = "live"
	KeyEnvironmentTest         KeyEnvironment = "test"
	KeyEnvironmentUnrecognized KeyEnvironment = "unrecognized"
)

type Client struct {
	Emails       *EmailsResource
	Templates    *TemplatesResource
	Attachments  *AttachmentsResource
	Domains      *DomainsResource
	Events       *EventsResource
	Inbound      *InboundResource
	Analytics    *AnalyticsResource
	Broadcasts   *BroadcastsResource
	Contacts     *ContactsResource
	Tags         *TagsResource
	Suppressions *SuppressionsResource
	Webhooks     *WebhooksResource
	APIKeys      *APIKeysResource

	apiKey         string
	baseURL        string
	userAgent      string
	httpClient     *http.Client
	ownsHTTPClient bool
	retryPolicy    RetryPolicy
	automaticKeys  bool
	defaultHeaders map[string]string
	now            func() time.Time
	random         func() float64
	configErr      error
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient == nil {
			return
		}

		c.httpClient = httpClient
		c.ownsHTTPClient = false
	}
}

func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
}

func WithRetryPolicy(policy RetryPolicy) Option {
	return func(c *Client) {
		c.retryPolicy = policy
	}
}

func WithAutomaticIdempotencyKeys(enabled bool) Option {
	return func(c *Client) {
		c.automaticKeys = enabled
	}
}

func WithUserAgentSuffix(suffix string) Option {
	return func(c *Client) {
		if suffix != "" {
			c.userAgent = c.userAgent + " " + suffix
		}
	}
}

func WithHeader(name, value string) Option {
	return func(c *Client) {
		c.defaultHeaders[name] = value
	}
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

func WithRandomSource(random func() float64) Option {
	return func(c *Client) {
		if random != nil {
			c.random = random
		}
	}
}

func New(apiKey string, opts ...Option) *Client {
	client := &Client{
		apiKey:         apiKey,
		baseURL:        ProductionBaseURL,
		userAgent:      "epostix-go/1.0.0",
		httpClient:     &http.Client{},
		ownsHTTPClient: true,
		retryPolicy:    DefaultRetryPolicy(),
		automaticKeys:  true,
		defaultHeaders: map[string]string{},
		now:            time.Now,
		random:         rand.Float64,
	}

	for _, opt := range opts {
		opt(client)
	}

	client.configErr = validateConfig(client)

	built := buildResources(client)
	client.Emails = built.Emails
	client.Templates = built.Templates
	client.Attachments = built.Attachments
	client.Domains = built.Domains
	client.Events = built.Events
	client.Inbound = built.Inbound
	client.Analytics = built.Analytics
	client.Broadcasts = built.Broadcasts
	client.Contacts = built.Contacts
	client.Tags = built.Tags
	client.Suppressions = built.Suppressions
	client.Webhooks = built.Webhooks
	client.APIKeys = built.APIKeys

	return client
}

func validateConfig(client *Client) error {
	if strings.TrimSpace(client.apiKey) == "" {
		return &ConfigError{Message: "an API key is required"}
	}

	if strings.HasPrefix(client.apiKey, "Bearer ") {
		return &ConfigError{Message: "pass the API key alone; the client adds the Bearer prefix itself"}
	}

	if !strings.HasPrefix(client.baseURL, "http://") && !strings.HasPrefix(client.baseURL, "https://") {
		return &ConfigError{Message: "baseURL must be an absolute http or https URL"}
	}

	return nil
}

func (c *Client) WithAPIKey(apiKey string) *Client {
	derived := New(apiKey,
		WithBaseURL(c.baseURL),
		WithRetryPolicy(c.retryPolicy),
		WithAutomaticIdempotencyKeys(c.automaticKeys),
		WithClock(c.now),
		WithRandomSource(c.random),
	)

	derived.httpClient = c.httpClient
	derived.ownsHTTPClient = false
	derived.userAgent = c.userAgent

	for name, value := range c.defaultHeaders {
		derived.defaultHeaders[name] = value
	}

	return derived
}

func (c *Client) KeyEnvironment() KeyEnvironment {
	switch {
	case strings.HasPrefix(c.apiKey, "tix_live_"):
		return KeyEnvironmentLive
	case strings.HasPrefix(c.apiKey, "tix_test_"):
		return KeyEnvironmentTest
	default:
		return KeyEnvironmentUnrecognized
	}
}

func (c *Client) CloseIdleConnections() {
	if c.ownsHTTPClient {
		c.httpClient.CloseIdleConnections()
	}
}
