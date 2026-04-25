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
	// cache em memória para reduzir chamadas na SWAPI
	starshipCache = make(map[string]CacheItem)

	// garante segurança em concorrência
	cacheMutex sync.RWMutex
)

func main() {
	println("SERVIDOR GO INICIANDO NA PORTA 8081")

	http.HandleFunc("/deathstar-analysis/", deathstarAnalysisHandler)

	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		panic(err)
	}
}

// busca informações da nave na SWAPI com retry e timeout
func getStarshipInfo(shipId string) (map[string]string, int) {
	url := "https://swapi.py4e.com/api/starships/" + shipId + "/"

	client := http.Client{
		Timeout: 3 * time.Second,
	}

	var resp *http.Response
	var err error

	// tenta recuperar dados externos com tolerância a falhas
	for attempt := 1; attempt <= 2; attempt++ {
		println("TENTATIVA:", attempt)

		resp, err = client.Get(url)

		// sucesso na chamada externa
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		println("FALHA NA TENTATIVA:", attempt)

		time.Sleep(200 * time.Millisecond)
	}

	// se todas as tentativas falharem, retorna erro controlado
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
		println("ERRO AO DECODIFICAR RESPOSTA:", err.Error())
		return nil, http.StatusBadGateway
	}

	// normaliza resposta da SWAPI para formato interno
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

	// normaliza o id da nave recebido na URL
	shipId := strings.TrimPrefix(path, prefix)
	shipId = strings.TrimSpace(shipId)
	shipId = strings.TrimSuffix(shipId, "/")

	if shipId == "" {
		http.NotFound(w, r)
		return
	}

	println("SHIP ID:", shipId)

	// leitura segura do cache
	cacheMutex.RLock()
	cached, ok := starshipCache[shipId]
	cacheMutex.RUnlock()

	// valida se existe cache válido
	if ok && time.Now().Before(cached.expiresAt) {
		println("CACHE HIT")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached.data)
		return
	}

	// caso não exista cache válido, busca na API externa
	println("CACHE MISS")

	info, errStatus := getStarshipInfo(shipId)
	if errStatus != 0 {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// escreve no cache com tempo de expiração
	cacheMutex.Lock()
	starshipCache[shipId] = CacheItem{
		data:      info,
		expiresAt: time.Now().Add(30 * time.Second),
	}
	cacheMutex.Unlock()

	// retorna resposta final para o cliente
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}