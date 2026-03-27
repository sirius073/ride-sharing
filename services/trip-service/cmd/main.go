package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"ride-sharing/shared/env"
	sharedtypes "ride-sharing/shared/types"

	"github.com/google/uuid"
)

type routeFare struct {
	ID                string            `json:"id"`
	PackageSlug       string            `json:"packageSlug"`
	BasePrice         float64           `json:"basePrice"`
	TotalPriceInCents float64           `json:"totalPriceInCents"`
	ExpiresAt         string            `json:"expiresAt"`
	Route             sharedtypes.Route `json:"route"`
}

type trip struct {
	ID           string            `json:"id"`
	UserID       string            `json:"userID"`
	Status       string            `json:"status"`
	SelectedFare *routeFare        `json:"selectedFare,omitempty"`
	Route        sharedtypes.Route `json:"route"`
}

type createTripRequest struct {
	UserID       string            `json:"userID"`
	Status       string            `json:"status"`
	SelectedFare *routeFare        `json:"selectedFare,omitempty"`
	Route        sharedtypes.Route `json:"route"`
}

type updateTripStatusRequest struct {
	Status string `json:"status"`
}

var (
	httpAddr = env.GetString("TRIP_HTTP_ADDR", ":8083")

	tripsMu sync.RWMutex
	trips   = map[string]trip{}
)

func main() {
	log.Println("Starting Trip Service")

	mux := http.NewServeMux()
	mux.HandleFunc("/internal/trips", handleCreateTrip)
	mux.HandleFunc("/internal/trips/", handleTripByID)

	server := &http.Server{
		Addr:    httpAddr,
		Handler: mux,
	}

	log.Printf("Trip Service is running on %s", httpAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("Trip Service failed to start: %v", err)
	}
}

func handleCreateTrip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req createTripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "Missing userID", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "trip.event.created"
	}

	createdTrip := trip{
		ID:           uuid.NewString(),
		UserID:       req.UserID,
		Status:       req.Status,
		SelectedFare: req.SelectedFare,
		Route:        req.Route,
	}

	tripsMu.Lock()
	trips[createdTrip.ID] = createdTrip
	tripsMu.Unlock()

	_ = writeJSON(w, http.StatusCreated, createdTrip)
}

func handleTripByID(w http.ResponseWriter, r *http.Request) {
	trimmedPath := strings.TrimPrefix(r.URL.Path, "/internal/trips/")
	if trimmedPath == "" {
		http.NotFound(w, r)
		return
	}

	segments := strings.Split(trimmedPath, "/")
	tripID := segments[0]

	if len(segments) == 1 && r.Method == http.MethodGet {
		handleGetTrip(w, tripID)
		return
	}

	if len(segments) == 2 && segments[1] == "status" && r.Method == http.MethodPatch {
		handlePatchTripStatus(w, r, tripID)
		return
	}

	http.Error(w, "Not found", http.StatusNotFound)
}

func handleGetTrip(w http.ResponseWriter, tripID string) {
	tripsMu.RLock()
	t, ok := trips[tripID]
	tripsMu.RUnlock()
	if !ok {
		http.Error(w, "Trip not found", http.StatusNotFound)
		return
	}

	_ = writeJSON(w, http.StatusOK, t)
}

func handlePatchTripStatus(w http.ResponseWriter, r *http.Request, tripID string) {
	defer r.Body.Close()

	var req updateTripStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		http.Error(w, "Missing status", http.StatusBadRequest)
		return
	}

	tripsMu.Lock()
	t, ok := trips[tripID]
	if !ok {
		tripsMu.Unlock()
		http.Error(w, "Trip not found", http.StatusNotFound)
		return
	}

	t.Status = req.Status
	trips[tripID] = t
	tripsMu.Unlock()

	_ = writeJSON(w, http.StatusOK, t)
}

func writeJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}
