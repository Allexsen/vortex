package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /search", handler)

	http.ListenAndServe(":8080", mux)
}

func handler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "missing query parameter 'q'", http.StatusBadRequest)
		return
	}

	req := map[string]interface{}{
		"text": q,
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "failed to marshal request", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://embedder:8000/embed", bytes.NewReader(reqJSON))
	if err != nil {
		http.Error(w, "failed to create request", http.StatusInternalServerError)
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		http.Error(w, "failed to send request", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var embedResp struct {
		Embedding []float64 `json:"embedding"`
	}
	err = json.NewDecoder(resp.Body).Decode(&embedResp)
	if err != nil {
		http.Error(w, "failed to decode response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(embedResp)
}
