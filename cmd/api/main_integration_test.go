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

func newTestAPI(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(route+"/", deathstarAnalysisHandler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func decodeResponse(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	return got
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

	api := newTestAPI(t)
	resp, err := api.Client().Get(api.URL + route + "/9")
	if err != nil {
		t.Fatalf("failed to call API: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	got := decodeResponse(t, resp)

	if got["ship"] != "Death Star" {
		t.Fatalf("expected ship Death Star, got %#v", got["ship"])
	}

	if got["model"] != "DS-1" {
		t.Fatalf("expected model DS-1, got %#v", got["model"])
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

	api := newTestAPI(t)
	resp, err := api.Client().Get(api.URL + route + "/9")
	if err != nil {
		t.Fatalf("failed to call API: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 with fallback, got %d", resp.StatusCode)
	}
	got := decodeResponse(t, resp)

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

	api := newTestAPI(t)

	firstResp, err := api.Client().Get(api.URL + route + "/15")
	if err != nil {
		t.Fatalf("failed to call API first time: %v", err)
	}
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first request expected status 200, got %d", firstResp.StatusCode)
	}
	firstGot := decodeResponse(t, firstResp)

	secondResp, err := api.Client().Get(api.URL + route + "/15")
	if err != nil {
		t.Fatalf("failed to call API second time: %v", err)
	}
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("second request expected status 200, got %d", secondResp.StatusCode)
	}
	secondGot := decodeResponse(t, secondResp)

	if atomic.LoadInt32(&upstreamCalls) != 1 {
		t.Fatalf("expected exactly 1 upstream call due to cache hit, got %d", upstreamCalls)
	}

	if secondGot["ship"] != "Executor" {
		t.Fatalf("expected cached ship Executor, got %#v", secondGot["ship"])
	}

	if secondGot["degraded"] != false {
		t.Fatalf("expected degraded false on cache hit, got %#v", secondGot["degraded"])
	}

	if firstGot["threatScore"] != secondGot["threatScore"] {
		t.Fatalf("expected cache hit to keep threatScore, got first=%#v second=%#v", firstGot["threatScore"], secondGot["threatScore"])
	}

	if firstGot["class"] != secondGot["class"] {
		t.Fatalf("expected cache hit to keep class, got first=%#v second=%#v", firstGot["class"], secondGot["class"])
	}

	if testutil.ToFloat64(cacheHitsMetric) <= beforeHits {
		t.Fatalf("expected cache_hits_total to increase")
	}
}
