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

type CacheItem struct {
	data      map[string]string
	expiresAt time.Time
}

var (
	// cache em memória para reduzir chamadas na SWAPI
	starshipCache = make(map[string]CacheItem)

	// garante segurança em concorrência
	cacheMutex sync.RWMutex

	// métricas simples legadas (mantidas para debug)
	cacheHits  int64
	cacheMiss  int64

	// =========================
	// PROMETHEUS METRICS
	// =========================

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

	// registra métricas Prometheus
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(cacheHitsMetric)
	prometheus.MustRegister(cacheMissMetric)
	prometheus.MustRegister(httpDuration)

	http.HandleFunc("/deathstar-analysis/", deathstarAnalysisHandler)
	http.Handle("/metrics", promhttp.Handler())

	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		panic(err)
	}
}

// log estruturado simples para rastrear eventos do sistema
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

// endpoint de métricas simples (legacy)
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"cache_hits": cacheHits,
		"cache_miss": cacheMiss,
	})
}

// busca informações da nave na SWAPI com retry e timeout
func getStarshipInfo(shipId string) (map[string]string, int) {
	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"

	client := http.Client{
		Timeout: 3 * time.Second,
	}

	var resp *http.Response
	var err error

	for attempt := 1; attempt <= 2; attempt++ {
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

		time.Sleep(200 * time.Millisecond)
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

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		logEvent("decode_error", shipId, nil)
		return nil, http.StatusBadGateway
	}

	result := map[string]string{
		"ship":       data.Name,
		"model":      data.Model,
		"crew":       data.Crew,
		"passengers": data.Passengers,
	}

	return result, 0
}

// handler principal da análise da estrela da morte
func deathstarAnalysisHandler(w http.ResponseWriter, r *http.Request) {

	start := time.Now()

	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		httpRequestsTotal.WithLabelValues("/deathstar-analysis", "405").Inc()
		return
	}

	path := r.URL.Path
	prefix := "/deathstar-analysis/"

	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		httpRequestsTotal.WithLabelValues("/deathstar-analysis", "404").Inc()
		return
	}

	shipId := strings.TrimPrefix(path, prefix)
	shipId = strings.TrimSpace(shipId)
	shipId = strings.TrimSuffix(shipId, "/")

	if shipId == "" {
		http.NotFound(w, r)
		httpRequestsTotal.WithLabelValues("/deathstar-analysis", "404").Inc()
		return
	}

	cacheMutex.RLock()
	cached, ok := starshipCache[shipId]
	cacheMutex.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		cacheHits++
		cacheHitsMetric.Inc()

		logEvent("cache_hit", shipId, nil)

		httpRequestsTotal.WithLabelValues("/deathstar-analysis", "200").Inc()

		httpDuration.WithLabelValues("/deathstar-analysis").Observe(time.Since(start).Seconds())

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached.data)
		return
	}

	cacheMiss++
	cacheMissMetric.Inc()

	logEvent("cache_miss", shipId, nil)

	info, errStatus := getStarshipInfo(shipId)
	if errStatus != 0 {
		httpRequestsTotal.WithLabelValues("/deathstar-analysis", "502").Inc()
		httpDuration.WithLabelValues("/deathstar-analysis").Observe(time.Since(start).Seconds())

		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data:      info,
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	httpRequestsTotal.WithLabelValues("/deathstar-analysis", "200").Inc()
	httpDuration.WithLabelValues("/deathstar-analysis").Observe(time.Since(start).Seconds())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}