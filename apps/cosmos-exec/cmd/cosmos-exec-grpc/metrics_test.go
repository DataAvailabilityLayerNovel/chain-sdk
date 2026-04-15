package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler(t *testing.T) {
	exec := newTestExecutor(t)
	m := newMetrics()

	// Increment some counters.
	m.incRequest()
	m.incRequest()
	m.incTxSubmit()
	m.incError()

	handler := metricsHandler(exec, m)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "cosmos_exec_requests_total 2") {
		t.Errorf("expected requests_total 2 in metrics output")
	}
	if !strings.Contains(body, "cosmos_exec_errors_total 1") {
		t.Errorf("expected errors_total 1 in metrics output")
	}
	if !strings.Contains(body, "cosmos_exec_tx_submits_total 1") {
		t.Errorf("expected tx_submits_total 1 in metrics output")
	}
	if !strings.Contains(body, "cosmos_exec_uptime_seconds") {
		t.Errorf("expected uptime_seconds in metrics output")
	}
}

func TestMetricsJSONHandler(t *testing.T) {
	exec := newTestExecutor(t)
	m := newMetrics()

	handler := metricsJSONHandler(exec, m)
	req := httptest.NewRequest(http.MethodGet, "/metrics.json", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json content type, got %q", ct)
	}
}

func TestMetricsCountingMiddleware(t *testing.T) {
	m := newMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // simulate an error
	})

	handler := metricsCountingMiddleware(inner, m)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if m.requestCount.Load() != 1 {
		t.Errorf("expected request count 1, got %d", m.requestCount.Load())
	}
	if m.errorCount.Load() != 1 {
		t.Errorf("expected error count 1, got %d", m.errorCount.Load())
	}
}
