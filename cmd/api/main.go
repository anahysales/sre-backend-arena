package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"sre-backend-arena/internal/service"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const route = "/deathstar-analysis"

var swapiBaseURL = "https://swapi.py4e.com/api/starships"

// cache simples com TTL
type CacheItem struct {
	data      map[string]interface{}
	expiresAt time.Time
}

var (
	starshipCache = make(map[string]CacheItem)
	cacheMutex    sync.RWMutex
)

// limita concorrência pra não explodir a SWAPI
var swapiSemaphore = make(chan struct{}, 20)

// rate limit simples (token bucket)
var rateLimiter = time.Tick(100 * time.Millisecond)

// circuit breaker básico
type CircuitBreaker struct {
	mu        sync.Mutex
	failures  int
	lastFail  time.Time
	state     string
	threshold int
	timeout   time.Duration
}

var cb = CircuitBreaker{
	state:     "CLOSED",
	threshold: 5,
	timeout:   10 * time.Second,
}

func (c *CircuitBreaker) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == "OPEN" {
		if time.Since(c.lastFail) > c.timeout {
			c.state = "CLOSED"
			c.failures = 0
			return true
		}
		return false
	}

	return true
}

func (c *CircuitBreaker) success() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures = 0
	c.state = "CLOSED"
}

func (c *CircuitBreaker) failure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures++
	c.lastFail = time.Now()

	if c.failures >= c.threshold {
		c.state = "OPEN"
	}
}

// métricas básicas
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "http_requests_total"},
		[]string{"path", "method", "status"},
	)

	cacheHitsMetric = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "cache_hits_total"},
	)

	cacheMissMetric = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "cache_miss_total"},
	)

	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method", "status"},
	)

	fallbackTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "fallback_total"},
	)

	circuitOpenTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "circuit_open_total"},
	)

	retryTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "retry_total"},
	)

	externalRateLimitedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "external_rate_limited_total"},
	)

	degradedResponsesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "degraded_responses_total"},
	)
)

func main() {
	fmt.Println("API rodando na porta 8081")

	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(cacheHitsMetric)
	prometheus.MustRegister(cacheMissMetric)
	prometheus.MustRegister(httpDuration)
	prometheus.MustRegister(fallbackTotal)
	prometheus.MustRegister(circuitOpenTotal)
	prometheus.MustRegister(retryTotal)
	prometheus.MustRegister(externalRateLimitedTotal)
	prometheus.MustRegister(degradedResponsesTotal)

	http.HandleFunc(route+"/", deathstarAnalysisHandler)
	http.HandleFunc("/health", healthHandler)
	http.Handle("/metrics", promhttp.Handler())

	panic(http.ListenAndServe(":8081", nil))
}

// log simples pra debug
func logEvent(event, traceId, shipId string, extra map[string]interface{}) {
	log := map[string]interface{}{
		"event":    event,
		"trace_id": traceId,
		"ship_id":  shipId,
		"time":     time.Now().Format(time.RFC3339),
		"extra":    extra,
	}

	b, _ := json.Marshal(log)
	fmt.Println(string(b))
}

// copia mapa para evitar race condition no cache
func cloneMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

// converte números da SWAPI para inteiro
func parseNumber(value string) int {
	clean := strings.ReplaceAll(value, ",", "")

	number, err := strconv.Atoi(clean)
	if err != nil {
		return 0
	}

	return number
}

// fallback padrão para resposta degradada
func fallbackStarshipInfo() map[string]string {
	return map[string]string{
		"ship":       "unknown",
		"model":      "unknown",
		"crew":       "0",
		"passengers": "0",
	}
}

// chama SWAPI com proteção avançada
func getStarshipInfo(traceId, shipId string) (map[string]string, int) {

	if shipId == "" {
		return nil, http.StatusBadRequest
	}

	if !cb.allow() {
		logEvent("circuit_open", traceId, shipId, nil)
		circuitOpenTotal.Inc()
		fallbackTotal.Inc()

		return fallbackStarshipInfo(), http.StatusOK
	}

	url := swapiBaseURL + "/" + shipId + "/"
	client := http.Client{Timeout: 3 * time.Second}

	var resp *http.Response
	var err error

	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			retryTotal.Inc()
		}

		<-rateLimiter
		externalRateLimitedTotal.Inc()

		swapiSemaphore <- struct{}{}
		resp, err = client.Get(url)
		<-swapiSemaphore

		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			cb.success()
			break
		}

		cb.failure()

		if resp != nil {
			resp.Body.Close()
		}

		backoff := time.Duration(100*(1<<i)) * time.Millisecond
		jitter := time.Duration(rand.Intn(100)) * time.Millisecond
		time.Sleep(backoff + jitter)
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		logEvent("swapi_fallback", traceId, shipId, nil)
		fallbackTotal.Inc()

		return fallbackStarshipInfo(), http.StatusOK
	}

	defer resp.Body.Close()

	var data struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Crew       string `json:"crew"`
		Passengers string `json:"passengers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		logEvent("decode_error", traceId, shipId, nil)
		fallbackTotal.Inc()

		return fallbackStarshipInfo(), http.StatusOK
	}

	return map[string]string{
		"ship":       data.Name,
		"model":      data.Model,
		"crew":       data.Crew,
		"passengers": data.Passengers,
	}, http.StatusOK
}

// handler principal
func deathstarAnalysisHandler(w http.ResponseWriter, r *http.Request) {

	start := time.Now()
	traceId := uuid.New().String()
	w.Header().Set("X-Trace-Id", traceId)

	status := "200"

	defer func() {
		httpDuration.WithLabelValues(route, r.Method, status).
			Observe(time.Since(start).Seconds())

		httpRequestsTotal.WithLabelValues(route, r.Method, status).Inc()
	}()

	if r.Method != http.MethodGet {
		status = "405"
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

		json.NewEncoder(w).Encode(cloneMap(cached.data))
		return
	}

	cacheMissMetric.Inc()
	logEvent("cache_miss", traceId, shipId, nil)

	info, _ := getStarshipInfo(traceId, shipId)

	score, classification := service.CalculateThreat(info["crew"], info["passengers"])

	crew := parseNumber(info["crew"])
	passengers := parseNumber(info["passengers"])

	// detecta degradação
	degraded := info["ship"] == "unknown"
	if degraded {
		degradedResponsesTotal.Inc()
	}

	resp := map[string]interface{}{
		"ship":           info["ship"],
		"model":          info["model"],
		"crew":           crew,
		"passengers":     passengers,
		"threatScore":    score,
		"classification": classification,
		"degraded":       degraded,
	}

	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data:      cloneMap(resp),
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	json.NewEncoder(w).Encode(resp)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}
