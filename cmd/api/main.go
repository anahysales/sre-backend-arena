package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"sync"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const route = "/deathstar-analysis"

type CacheItem struct {
	data      map[string]string
	expiresAt time.Time
}

var (
	starshipCache = make(map[string]CacheItem)
	cacheMutex    sync.RWMutex

	// =========================
	// RATE LIMIT (GLOBAL)
	// =========================
	rateLimiter = time.Tick(200 * time.Millisecond) // 5 req/s

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total de requisições HTTP",
		},
		[]string{"path", "status"},
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
		[]string{"path"},
	)
)

func main() {
	println("SERVIDOR GO INICIANDO NA PORTA 8081")

	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(cacheHitsMetric)
	prometheus.MustRegister(cacheMissMetric)
	prometheus.MustRegister(httpDuration)

	http.HandleFunc(route+"/", deathstarAnalysisHandler)
	http.HandleFunc("/health", healthHandler) // ✅ NOVO
	http.Handle("/metrics", promhttp.Handler())

	panic(http.ListenAndServe(":8081", nil))
}

func logEvent(event string, shipId string, extra map[string]interface{}) {
	log := map[string]interface{}{
		"event":     event,
		"ship_id":   shipId,
		"timestamp": time.Now().Format(time.RFC3339),
		"extra":     extra,
	}

	data, _ := json.Marshal(log)
	fmt.Println(string(data))
}

func getStarshipInfo(shipId string) (map[string]string, int) {
	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"

	client := http.Client{Timeout: 3 * time.Second}

	var resp *http.Response
	var err error

	for attempt := 1; attempt <= 2; attempt++ {

		// RATE LIMIT
		<-rateLimiter

		logEvent("retry_attempt", shipId, map[string]interface{}{
			"attempt": attempt,
		})

		resp, err = client.Get(url)

		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		// BACKOFF EXPONENCIAL
		time.Sleep(time.Duration(200*attempt) * time.Millisecond)
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		logEvent("swapi_error", shipId, nil)
		return nil, http.StatusBadGateway
	}

	defer resp.Body.Close()

	logEvent("swapi_response", shipId, map[string]interface{}{
		"status_code": resp.StatusCode,
	})

	var data struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Crew       string `json:"crew"`
		Passengers string `json:"passengers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		logEvent("decode_error", shipId, nil)
		return nil, http.StatusBadGateway
	}

	return map[string]string{
		"ship":       data.Name,
		"model":      data.Model,
		"crew":       data.Crew,
		"passengers": data.Passengers,
	}, 0
}

func deathstarAnalysisHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodGet {
		httpRequestsTotal.WithLabelValues(route, "405").Inc()
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !strings.HasPrefix(r.URL.Path, route+"/") {
		httpRequestsTotal.WithLabelValues(route, "404").Inc()
		http.NotFound(w, r)
		return
	}

	shipId := strings.TrimPrefix(r.URL.Path, route+"/")
	shipId = strings.Trim(shipId, "/ ")

	if shipId == "" {
		httpRequestsTotal.WithLabelValues(route, "404").Inc()
		http.NotFound(w, r)
		return
	}

	cacheMutex.RLock()
	cached, ok := starshipCache[shipId]
	cacheMutex.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		cacheHitsMetric.Inc()

		logEvent("cache_hit", shipId, nil)

		httpRequestsTotal.WithLabelValues(route, "200").Inc()
		httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached.data)
		return
	}

	cacheMissMetric.Inc()
	logEvent("cache_miss", shipId, nil)

	info, errStatus := getStarshipInfo(shipId)
	if errStatus != 0 {
		httpRequestsTotal.WithLabelValues(route, "502").Inc()
		httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())

		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data:      info,
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	httpRequestsTotal.WithLabelValues(route, "200").Inc()
	httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// =========================
// HEALTH CHECK
// =========================
func healthHandler(w http.ResponseWriter, r *http.Request) {
	client := http.Client{Timeout: 1 * time.Second}

	resp, err := client.Get("https://swapi.py4e.com/api/starships/9/")
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "dependency failure", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}