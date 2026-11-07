package agentlog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// recorder is a minimal OTLP/HTTP collector for tests.
type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
	// statuses lets a test script the response codes; the last one repeats.
	statuses []int
}

type recordedRequest struct {
	path        string
	contentType string
	body        []byte
}

func (r *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, recordedRequest{
			path:        req.URL.Path,
			contentType: req.Header.Get("Content-Type"),
			body:        body,
		})
		status := http.StatusOK
		if len(r.statuses) > 0 {
			status = r.statuses[0]
			if len(r.statuses) > 1 {
				r.statuses = r.statuses[1:]
			}
		}
		r.mu.Unlock()
		w.WriteHeader(status)
	})
}

func (r *recorder) snapshot() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

func sampleBatch(t *testing.T) map[string]interface{} {
	t.Helper()
	info := baseEvent("log", LevelInfo)
	info["message"] = "hello"
	request := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	request["payload"] = map[string]interface{}{
		"method": "GET", "url": "https://api.example.com/x", "status": 200, "duration_ms": 5,
	}
	batch, err := ToOTLP([]interface{}{info, request}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func newTestExporter(endpoint string) *OTLPExporter {
	exporter := NewOTLPExporter(endpoint)
	exporter.Backoff = time.Millisecond
	return exporter
}

func TestExportPostsBothSignals(t *testing.T) {
	srv := &recorder{}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	exporter := newTestExporter(server.URL)
	if err := exporter.Export(context.Background(), sampleBatch(t)); err != nil {
		t.Fatalf("Export: %v", err)
	}

	requests := srv.snapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2 (logs + traces)", len(requests))
	}
	if requests[0].path != "/v1/logs" {
		t.Errorf("first path = %q, want /v1/logs", requests[0].path)
	}
	if requests[1].path != "/v1/traces" {
		t.Errorf("second path = %q, want /v1/traces", requests[1].path)
	}
	for i, request := range requests {
		if request.contentType != "application/json" {
			t.Errorf("request %d content-type = %q", i, request.contentType)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(request.body, &decoded); err != nil {
			t.Fatalf("request %d body is not JSON: %v", i, err)
		}
	}
}

func TestExportSkipsEmptySignals(t *testing.T) {
	srv := &recorder{}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	// Only a plain log → no span → the traces signal must be skipped entirely.
	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := newTestExporter(server.URL).Export(context.Background(), batch); err != nil {
		t.Fatalf("Export: %v", err)
	}

	requests := srv.snapshot()
	if len(requests) != 1 || requests[0].path != "/v1/logs" {
		t.Fatalf("expected only a logs request, got %+v", requests)
	}
}

func TestExportRetriesOn503(t *testing.T) {
	srv := &recorder{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	exporter := newTestExporter(server.URL)
	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.Export(context.Background(), batch); err != nil {
		t.Fatalf("Export should have retried and succeeded: %v", err)
	}
	if got := len(srv.snapshot()); got != 2 {
		t.Errorf("attempts = %d, want 2 (503 then 200)", got)
	}
}

func TestExportRetriesOn429(t *testing.T) {
	srv := &recorder{statuses: []int{http.StatusTooManyRequests, http.StatusOK}}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := newTestExporter(server.URL).Export(context.Background(), batch); err != nil {
		t.Fatalf("429 should be retryable: %v", err)
	}
	if got := len(srv.snapshot()); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestExportDoesNotRetryOn400(t *testing.T) {
	srv := &recorder{statuses: []int{http.StatusBadRequest}}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = newTestExporter(server.URL).Export(context.Background(), batch)
	if err == nil {
		t.Fatal("400 must surface as an error")
	}
	if got := len(srv.snapshot()); got != 1 {
		t.Errorf("attempts = %d, want 1 (4xx is permanent)", got)
	}
}

func TestExportGivesUpAfterMaxRetries(t *testing.T) {
	srv := &recorder{statuses: []int{http.StatusInternalServerError}}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exporter := newTestExporter(server.URL)
	exporter.MaxRetries = 2
	if err := exporter.Export(context.Background(), batch); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if got := len(srv.snapshot()); got != 3 {
		t.Errorf("attempts = %d, want 3 (1 + 2 retries)", got)
	}
}

func TestExportSinkDrainsOnce(t *testing.T) {
	srv := &recorder{}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	sink := NewOTLPSink(nil)
	logger := NewLogger("svc", sink)
	logger.Info("one")
	logger.RecordAuto(string(EventTypeDBQuery), "SELECT 1",
		WithPayload(map[string]interface{}{"statement": "SELECT 1", "duration_ms": 2}))

	exporter := newTestExporter(server.URL)
	if err := exporter.ExportSink(context.Background(), sink); err != nil {
		t.Fatalf("ExportSink: %v", err)
	}
	if sink.Len() != 0 {
		t.Errorf("sink should be drained, %d lines left", sink.Len())
	}
	if got := len(srv.snapshot()); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}

	// A second export has nothing to send and must not hit the network.
	if err := exporter.ExportSink(context.Background(), sink); err != nil {
		t.Fatalf("second ExportSink: %v", err)
	}
	if got := len(srv.snapshot()); got != 2 {
		t.Errorf("empty drain should send nothing, got %d requests", got)
	}
}

func TestExporterEndpointNormalization(t *testing.T) {
	for _, endpoint := range []string{"http://collector:4318", "http://collector:4318/", " http://collector:4318// "} {
		exporter := NewOTLPExporter(endpoint)
		if got := exporter.urlFor(otlpLogsPath); got != "http://collector:4318/v1/logs" {
			t.Errorf("endpoint %q -> %q", endpoint, got)
		}
	}
	if got := NewOTLPExporter("").Endpoint; got != DefaultOTLPEndpoint {
		t.Errorf("empty endpoint -> %q", got)
	}
}

func TestExporterCustomHeaders(t *testing.T) {
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = req.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exporter := newTestExporter(server.URL)
	exporter.Headers = map[string]string{"X-Api-Key": "secret"}
	if err := exporter.Export(context.Background(), batch); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := seen.Get("X-Api-Key"); got != "secret" {
		t.Errorf("X-Api-Key header = %q", got)
	}
}

func TestExporterHonoursCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	event := baseEvent("log", LevelInfo)
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exporter := newTestExporter(server.URL)
	exporter.Backoff = time.Hour // would block forever without ctx handling
	if err := exporter.Export(ctx, batch); err == nil {
		t.Fatal("cancelled context should abort the export")
	}
}

func TestBackoffIsExponentialAndCapped(t *testing.T) {
	exporter := &OTLPExporter{Backoff: 10 * time.Millisecond}
	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
	}
	for i, expected := range want {
		if got := exporter.backoffFor(i + 1); got != expected {
			t.Errorf("backoffFor(%d) = %v, want %v", i+1, got, expected)
		}
	}
	// Never grow unbounded (capped at 64x).
	if got := exporter.backoffFor(30); got > 10*time.Millisecond*64 {
		t.Errorf("backoff not capped: %v", got)
	}
}
