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

// cache simples com expiração
type CacheItem struct {
	data      map[string]string
	expiresAt time.Time
}

var (
	starshipCache = make(map[string]CacheItem)
	cacheMutex    sync.RWMutex
)

// controle de concorrência para chamadas externas
var swapiSemaphore = make(chan struct{}, 20)

// limite simples de taxa para chamadas na SWAPI
var rateLimiter = time.NewTicker(50 * time.Millisecond)

// circuit breaker básico
type CircuitBreaker struct {
	mu sync.Mutex

	failures int
	lastFail time.Time

	state string

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
			c.state = "HALF"
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

// métricas Prometheus
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
)

func main() {
	fmt.Println("API rodando na porta 8081")

	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(cacheHitsMetric)
	prometheus.MustRegister(cacheMissMetric)
	prometheus.MustRegister(httpDuration)

	http.HandleFunc(route+"/", deathstarAnalysisHandler)
	http.HandleFunc("/health", healthHandler)
	http.Handle("/metrics", promhttp.Handler())

	panic(http.ListenAndServe(":8081", nil))
}

// logs estruturados simples
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

// cálculo de risco da nave
func calculateThreat(crewStr, passengersStr string) (int, string) {
	parse := func(s string) int {
		s = strings.ReplaceAll(s, ",", "")
		v, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return v
	}

	score := (parse(crewStr) + parse(passengersStr)) / 10000
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
		return score, "critical"
	}
}

// chamada para SWAPI com proteção
func getStarshipInfo(traceId, shipId string) (map[string]string, int) {

	if shipId == "" {
		return nil, http.StatusBadRequest
	}

	if !cb.allow() {
		logEvent("circuit_open", traceId, shipId, nil)
		return nil, http.StatusServiceUnavailable
	}

	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"
	client := http.Client{Timeout: 3 * time.Second}

	var resp *http.Response
	var err error

	for i := 1; i <= 2; i++ {

		swapiSemaphore <- struct{}{}
		<-rateLimiter.C

		resp, err = client.Get(url)

		if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
			cb.failure()
		} else {
			cb.success()
		}

		if resp != nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		time.Sleep(time.Duration(200*i) * time.Millisecond)

		<-swapiSemaphore
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		logEvent("swapi_error", traceId, shipId, nil)
		return nil, http.StatusBadGateway
	}

	defer resp.Body.Close()

	var data struct {
		Name       string
		Model      string
		Crew       string
		Passengers string
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

// handler principal da API
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

		json.NewEncoder(w).Encode(cached.data)
		return
	}

	cacheMissMetric.Inc()
	logEvent("cache_miss", traceId, shipId, nil)

	info, errStatus := getStarshipInfo(traceId, shipId)
	if errStatus != 0 {
		status = "502"
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	score, class := calculateThreat(info["crew"], info["passengers"])

	resp := map[string]interface{}{
		"ship":        info["ship"],
		"model":       info["model"],
		"crew":        info["crew"],
		"passengers":  info["passengers"],
		"threatScore": score,
		"class":       class,
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

	json.NewEncoder(w).Encode(resp)
}

// health check simples
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}