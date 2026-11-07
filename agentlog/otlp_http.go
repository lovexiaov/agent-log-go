package agentlog

// OTLP/HTTP exporter: sends the batches produced by the OTLP adapter to a
// collector over OTLP/JSON (the "http/json" encoding).
//
// Scope: this is a deliberately small transport, not a queue. It performs the
// OTLP/HTTP request shape (one signal per request), a bounded retry with
// exponential backoff for retryable failures, and nothing else — no batching
// timer, no disk buffer, no backpressure. For high-volume production, point the
// exporter at a local Collector and let the Collector own durability.
//
// Retry policy (mirrors OTLP spec guidance):
//
//	2xx              -> success
//	429 / 408 / 5xx  -> retryable (exponential backoff)
//	other 4xx        -> permanent: the payload is malformed or unauthorised,
//	                    retrying would only multiply the error
//	transport error  -> retryable
//
// Zero third-party dependencies: net/http is standard library.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOTLPEndpoint is the default OTLP/HTTP collector address (the port the
// OpenTelemetry Collector listens on for OTLP/HTTP).
const DefaultOTLPEndpoint = "http://localhost:4318"

// OTLP signal paths, relative to the endpoint.
const (
	otlpLogsPath   = "/v1/logs"
	otlpTracesPath = "/v1/traces"
)

// Default retry knobs. Kept small on purpose: an agent-log exporter is expected
// to sit next to a local collector, where failures are transient and short.
const (
	defaultOTLPMaxRetries = 2 // 1 initial attempt + 2 retries
	defaultOTLPBackoff    = 100 * time.Millisecond
	defaultOTLPTimeout    = 10 * time.Second
)

// OTLPExporter posts OTLP/JSON batches to a collector.
//
//	exporter := agentlog.NewOTLPExporter("http://localhost:4318")
//	sink := agentlog.NewOTLPSink(nil)
//	logger := agentlog.NewLogger("svc", sink)
//	// ... emit events ...
//	batch, _ := sink.Drain()
//	_ = exporter.Export(ctx, batch)
type OTLPExporter struct {
	// Endpoint is the collector base URL, e.g. "http://localhost:4318".
	Endpoint string
	// Client overrides the HTTP client (tests, custom transport, TLS config).
	Client *http.Client
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// Backoff is the base delay; it doubles per retry.
	Backoff time.Duration
	// Headers are added to every request (e.g. an API key for a hosted backend).
	Headers map[string]string
}

// NewOTLPExporter returns an exporter with defaults applied. An empty endpoint
// falls back to DefaultOTLPEndpoint.
func NewOTLPExporter(endpoint string) *OTLPExporter {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultOTLPEndpoint
	}
	return &OTLPExporter{
		Endpoint:   endpoint,
		MaxRetries: defaultOTLPMaxRetries,
		Backoff:    defaultOTLPBackoff,
	}
}

// Export sends every non-empty signal in a batch produced by ToOTLP/Drain.
//
// Logs are sent before traces; the first permanent error aborts the batch so a
// caller can surface it instead of silently dropping data.
func (e *OTLPExporter) Export(ctx context.Context, batch map[string]interface{}) error {
	if payload, ok := batch["logs"]; ok {
		if err := e.ExportLogs(ctx, payload); err != nil {
			return err
		}
	}
	if payload, ok := batch["traces"]; ok {
		if err := e.ExportTraces(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// ExportSink drains a sink and exports the resulting batch.
func (e *OTLPExporter) ExportSink(ctx context.Context, sink *OTLPSink) error {
	if sink == nil {
		return fmt.Errorf("agentlog: nil OTLPSink")
	}
	batch, err := sink.Drain()
	if err != nil {
		return err
	}
	return e.Export(ctx, batch)
}

// ExportLogs sends one ExportLogsServiceRequest body. An empty payload (no
// resourceLogs) is a no-op, so callers need not check beforehand.
func (e *OTLPExporter) ExportLogs(ctx context.Context, payload interface{}) error {
	return e.exportSignal(ctx, otlpLogsPath, payload, "resourceLogs")
}

// ExportTraces sends one ExportTraceServiceRequest body.
func (e *OTLPExporter) ExportTraces(ctx context.Context, payload interface{}) error {
	return e.exportSignal(ctx, otlpTracesPath, payload, "resourceSpans")
}

func (e *OTLPExporter) exportSignal(ctx context.Context, path string, payload interface{}, emptyKey string) error {
	if isEmptySignal(payload, emptyKey) {
		return nil
	}
	body := []byte(ToOTLPJSON(payload))
	return e.post(ctx, path, body)
}

// isEmptySignal reports whether the payload carries no resource groups.
func isEmptySignal(payload interface{}, emptyKey string) bool {
	if payload == nil {
		return true
	}
	object, ok := payload.(map[string]interface{})
	if !ok {
		return false
	}
	groups, ok := object[emptyKey]
	if !ok || groups == nil {
		return true
	}
	if list, isList := groups.([]interface{}); isList {
		return len(list) == 0
	}
	return false
}

// post performs one signal POST with bounded retries.
func (e *OTLPExporter) post(ctx context.Context, path string, body []byte) error {
	attempts := e.maxRetries() + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := e.backoffFor(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		retryable, err := e.attempt(ctx, path, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return fmt.Errorf("agentlog: OTLP export gave up after %d attempts: %w", attempts, lastErr)
}

// attempt performs a single request. The boolean reports whether a failure is
// worth retrying.
func (e *OTLPExporter) attempt(ctx context.Context, path string, body []byte) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.urlFor(path), bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("agentlog: cannot build OTLP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range e.Headers {
		request.Header.Set(key, value)
	}

	response, err := e.client().Do(request)
	if err != nil {
		// Transport errors (connection refused, reset, timeout) are transient.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return true, fmt.Errorf("agentlog: OTLP request to %s failed: %w", path, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return false, nil
	}
	return isRetryableOTLPStatus(response.StatusCode), fmt.Errorf(
		"agentlog: OTLP %s returned %d %s",
		path, response.StatusCode, strings.TrimSpace(string(detail)),
	)
}

// isRetryableOTLPStatus follows the OTLP retryable-code guidance.
func isRetryableOTLPStatus(code int) bool {
	switch {
	case code == http.StatusTooManyRequests: // 429
		return true
	case code == http.StatusRequestTimeout: // 408
		return true
	case code >= 500: // 500..599
		return true
	}
	return false
}

func (e *OTLPExporter) urlFor(path string) string {
	return strings.TrimRight(strings.TrimSpace(e.Endpoint), "/") + path
}

func (e *OTLPExporter) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: defaultOTLPTimeout}
}

func (e *OTLPExporter) maxRetries() int {
	if e.MaxRetries < 0 {
		return 0
	}
	return e.MaxRetries
}

// backoffFor returns the delay before the given retry (1-based): exponential
// doubling, capped so a misconfigured MaxRetries cannot stall for minutes.
func (e *OTLPExporter) backoffFor(retry int) time.Duration {
	base := e.Backoff
	if base <= 0 {
		base = defaultOTLPBackoff
	}
	shift := retry - 1
	if shift > 6 { // cap at 64x
		shift = 6
	}
	return base * time.Duration(1<<uint(shift))
}
