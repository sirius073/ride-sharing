package main

import (
	"encoding/json"
	"net/http"
	"ride-sharing/shared/contracts"
)

func writeJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

func writeAPIResponse(w http.ResponseWriter, status int, data any) error {
	resp := contracts.APIResponse{
		Data: data,
	}
	return writeJSON(w, status, resp)
}
