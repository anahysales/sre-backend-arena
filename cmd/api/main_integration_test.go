package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func resetTestState() {
	cacheMutex.Lock()
	starshipCache = make(map[string]CacheItem)
	cacheMutex.Unlock()

	cb.mu.Lock()
	cb.failures = 0
	cb.lastFail = time.Time{}
	cb.state = "CLOSED"
	cb.mu.Unlock()
}

func TestDeathstarAnalysisSuccessReal(t *testing.T) {
	resetTestState()

	originalBaseURL := swapiBaseURL
	defer func() { swapiBaseURL = originalBaseURL }()

	swapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Death Star","model":"DS-1","crew":"342953","passengers":"843342"}`))
	}))
	defer swapi.Close()

	swapiBaseURL = swapi.URL

	req := httptest.NewRequest(http.MethodGet, route+"/9", nil)
	rec := httptest.NewRecorder()

	deathstarAnalysisHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got["ship"] != "Death Star" {
		t.Fatalf("expected ship Death Star, got %#v", got["ship"])
	}

	if got["degraded"] != false {
		t.Fatalf("expected degraded false, got %#v", got["degraded"])
	}
}

func TestDeathstarAnalysisFallbackWhenSWAPIFails(t *testing.T) {
	resetTestState()

	originalBaseURL := swapiBaseURL
	defer func() { swapiBaseURL = originalBaseURL }()

	swapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failure", http.StatusInternalServerError)
	}))
	defer swapi.Close()

	swapiBaseURL = swapi.URL

	beforeFallback := testutil.ToFloat64(fallbackTotal)
	beforeRetry := testutil.ToFloat64(retryTotal)

	req := httptest.NewRequest(http.MethodGet, route+"/9", nil)
	rec := httptest.NewRecorder()
	deathstarAnalysisHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 with fallback, got %d", rec.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got["ship"] != "unknown" {
		t.Fatalf("expected fallback ship unknown, got %#v", got["ship"])
	}

	if got["degraded"] != true {
		t.Fatalf("expected degraded true on fallback, got %#v", got["degraded"])
	}

	if testutil.ToFloat64(fallbackTotal) <= beforeFallback {
		t.Fatalf("expected fallback_total to increase")
	}

	if testutil.ToFloat64(retryTotal) <= beforeRetry {
		t.Fatalf("expected retry_total to increase")
	}
}

func TestDeathstarAnalysisCacheHit(t *testing.T) {
	resetTestState()

	originalBaseURL := swapiBaseURL
	defer func() { swapiBaseURL = originalBaseURL }()

	var upstreamCalls int32
	swapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Executor","model":"Executor-class","crew":"279144","passengers":"38000"}`))
	}))
	defer swapi.Close()

	swapiBaseURL = swapi.URL
	beforeHits := testutil.ToFloat64(cacheHitsMetric)

	firstReq := httptest.NewRequest(http.MethodGet, route+"/15", nil)
	firstRec := httptest.NewRecorder()
	deathstarAnalysisHandler(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("first request expected status 200, got %d", firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, route+"/15", nil)
	secondRec := httptest.NewRecorder()
	deathstarAnalysisHandler(secondRec, secondReq)

	if secondRec.Code != http.StatusOK {
		t.Fatalf("second request expected status 200, got %d", secondRec.Code)
	}

	if atomic.LoadInt32(&upstreamCalls) != 1 {
		t.Fatalf("expected exactly 1 upstream call due to cache hit, got %d", upstreamCalls)
	}

	if testutil.ToFloat64(cacheHitsMetric) <= beforeHits {
		t.Fatalf("expected cache_hits_total to increase")
	}
}
