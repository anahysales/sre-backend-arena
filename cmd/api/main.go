package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func main() {
	println("SERVIDOR GO INICIANDO NA PORTA 8081")

	http.HandleFunc("/deathstar-analysis/", deathstarAnalysisHandler)

	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		panic(err)
	}
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

	shipId := strings.TrimPrefix(path, prefix)
	if shipId == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]string{"shipId": shipId}
	json.NewEncoder(w).Encode(response)
}