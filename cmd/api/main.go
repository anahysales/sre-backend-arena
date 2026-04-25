package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"sync"
)

type CacheItem struct {
	data      map[string]string
	expiresAt time.Time
}

var (
	starshipCache = make(map[string]CacheItem)
	cacheMutex    sync.RWMutex
)

func main() {
	println("SERVIDOR GO INICIANDO NA PORTA 8081")

	http.HandleFunc("/deathstar-analysis/", deathstarAnalysisHandler)

	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		panic(err)
	}
}

func getStarshipInfo(shipId string) (map[string]string, int) {
	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"

	client := http.Client{
		Timeout: 3 * time.Second,
	}

	var resp *http.Response
	var err error

	// 🔁 RETRY (2 tentativas)
	for attempt := 1; attempt <= 2; attempt++ {
		println("TENTATIVA:", attempt)

		resp, err = client.Get(url)

		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		println("FALHA NA TENTATIVA:", attempt)

		time.Sleep(200 * time.Millisecond)
	}

	// ❌ falhou após retry
	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		println("ERRO APÓS RETRY")
		return nil, http.StatusBadGateway
	}

	defer resp.Body.Close()

	println("STATUS CODE:", resp.StatusCode)

	var data struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Crew       string `json:"crew"`
		Passengers string `json:"passengers"`
	}

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		println("ERROR DECODE:", err.Error())
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

func deathstarAnalysisHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	prefix := "/deathstar-analysis/"

	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}

	// 🔥 normalização do ID
	shipId := strings.TrimPrefix(path, prefix)
	shipId = strings.TrimSpace(shipId)
	shipId = strings.TrimSuffix(shipId, "/")

	if shipId == "" {
		http.NotFound(w, r)
		return
	}

	println("SHIP ID:", shipId)

	// 🔵 CACHE HIT
	cacheMutex.RLock()
	cached, ok := starshipCache[shipId]
	cacheMutex.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		println("CACHE HIT")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached.data)
		return
	}

	// 🔴 CACHE MISS
	println("CACHE MISS")

	info, errStatus := getStarshipInfo(shipId)
	if errStatus != 0 {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// 🔵 CACHE WRITE (TTL 30s)
	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data:      info,
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}