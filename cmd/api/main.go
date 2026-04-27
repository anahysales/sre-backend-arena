package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"sync"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const route = "/deathstar-analysis"

// =========================
// CACHE
// =========================

type CacheItem struct {
	data      map[string]string
	expiresAt time.Time
}

var (
	starshipCache = make(map[string]CacheItem)
	cacheMutex    sync.RWMutex
)

// =========================
// BULKHEAD
// =========================
var swapiSemaphore = make(chan struct{}, 20)

// =========================
// RATE LIMITER (CORRIGIDO)
// =========================
var rateLimiter = time.NewTicker(200 * time.Millisecond)

// =========================
// METRICS
// =========================
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total de requisições HTTP",
		},
		[]string{"path", "method", "status"},
	)

	cacheHitsMetric = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total de cache hits",
		},
	)

	cacheMissMetric = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "cache_miss_total",
			Help: "Total de cache misses",
		},
	)

	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duração das requisições HTTP",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method", "status"},
	)
)

func main() {
	println("SERVIDOR GO INICIANDO NA PORTA 8081")

	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(cacheHitsMetric)
	prometheus.MustRegister(cacheMissMetric)
	prometheus.MustRegister(httpDuration)

	http.HandleFunc(route+"/", deathstarAnalysisHandler)
	http.HandleFunc("/health", healthHandler)
	http.Handle("/metrics", promhttp.Handler())

	panic(http.ListenAndServe(":8081", nil))
}

// =========================
// LOG
// =========================
func logEvent(event string, traceId string, shipId string, extra map[string]interface{}) {
	log := map[string]interface{}{
		"event":     event,
		"trace_id":  traceId,
		"ship_id":   shipId,
		"timestamp": time.Now().Format(time.RFC3339),
		"extra":     extra,
	}

	data, _ := json.Marshal(log)
	fmt.Println(string(data))
}

// =========================
// THREAT SCORE
// =========================
func calculateThreat(crewStr, passengersStr string) (int, string) {
	parse := func(s string) int {
		s = strings.ReplaceAll(s, ",", "")
		val, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return val
	}

	crew := parse(crewStr)
	passengers := parse(passengersStr)

	score := (crew + passengers) / 10000
	if score > 100 {
		score = 100
	}

	switch {
	case score < 20:
		return score, "low_threat"
	case score < 50:
		return score, "medium_threat"
	case score < 80:
		return score, "high_threat"
	default:
		return score, "galactic_superweapon"
	}
}

// =========================
// SWAPI
// =========================
func getStarshipInfo(traceId string, shipId string) (map[string]string, int) {

	if shipId == "" {
		return nil, http.StatusBadRequest
	}

	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"
	client := http.Client{Timeout: 3 * time.Second}

	var resp *http.Response
	var err error

	for attempt := 1; attempt <= 2; attempt++ {

		<-swapiSemaphore
		<-rateLimiter.C

		logEvent("retry_attempt", traceId, shipId, map[string]interface{}{
			"attempt": attempt,
		})

		resp, err = client.Get(url)

		if resp != nil {
			defer resp.Body.Close()
		}

		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			swapiSemaphore <- struct{}{}
			break
		}

		time.Sleep(time.Duration(200*attempt) * time.Millisecond)
		swapiSemaphore <- struct{}{}
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		logEvent("swapi_error", traceId, shipId, nil)
		return nil, http.StatusBadGateway
	}

	var data struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Crew       string `json:"crew"`
		Passengers string `json:"passengers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		logEvent("decode_error", traceId, shipId, nil)
		return nil, http.StatusBadGateway
	}

	return map[string]string{
		"ship":       data.Name,
		"model":      data.Model,
		"crew":       data.Crew,
		"passengers": data.Passengers,
	}, 0
}

// =========================
// HANDLER
// =========================
func deathstarAnalysisHandler(w http.ResponseWriter, r *http.Request) {

	start := time.Now()
	traceId := uuid.New().String()
	w.Header().Set("X-Trace-Id", traceId)

	status := "200"

	defer func() {
		httpDuration.WithLabelValues(route, r.Method, status).Observe(time.Since(start).Seconds())
		httpRequestsTotal.WithLabelValues(route, r.Method, status).Inc()
	}()

	if r.Method != http.MethodGet {
		status = "405"
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	shipId := strings.TrimPrefix(r.URL.Path, route+"/")
	if shipId == "" {
		status = "400"
		http.Error(w, "missing ship id", http.StatusBadRequest)
		return
	}

	cacheMutex.RLock()
	cached, ok := starshipCache[shipId]
	cacheMutex.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		cacheHitsMetric.Inc()
		logEvent("cache_hit", traceId, shipId, nil)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached.data)
		return
	}

	cacheMissMetric.Inc()
	logEvent("cache_miss", traceId, shipId, nil)

	info, errStatus := getStarshipInfo(traceId, shipId)
	if errStatus != 0 {
		status = "502"
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	score, classification := calculateThreat(info["crew"], info["passengers"])

	response := map[string]interface{}{
		"ship":           info["ship"],
		"model":          info["model"],
		"crew":           info["crew"],
		"passengers":     info["passengers"],
		"threatScore":    score,
		"classification": classification,
	}

	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data: map[string]string{
			"ship":       info["ship"],
			"model":      info["model"],
			"crew":       info["crew"],
			"passengers": info["passengers"],
		},
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// =========================
// HEALTH CHECK (AJUSTADO)
// =========================
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}