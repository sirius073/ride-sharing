package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"time"

	sharedtypes "ride-sharing/shared/types"

	"github.com/google/uuid"
)

var (
	faresMu   sync.RWMutex
	faresByID = map[string]routeFare{}
)

func handleTripPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req previewTripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "Missing user_id", http.StatusBadRequest)
		return
	}

	if !isValidCoordinate(req.Pickup) || !isValidCoordinate(req.Destination) {
		http.Error(w, "Invalid pickup/destination coordinates", http.StatusBadRequest)
		return
	}

	tripRoute := buildRoute(req.Pickup, req.Destination)
	rideFares := buildRideFares(tripRoute)

	faresMu.Lock()
	for _, fare := range rideFares {
		faresByID[fare.ID] = fare
	}
	faresMu.Unlock()

	payload := struct {
		Route     sharedtypes.Route `json:"route"`
		RideFares []routeFare       `json:"rideFares"`
	}{
		Route:     tripRoute,
		RideFares: rideFares,
	}

	if err := writeAPIResponse(w, http.StatusOK, payload); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		return
	}
}

func handleTripStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req startTripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "Missing user_id", http.StatusBadRequest)
		return
	}
	if req.RideFareID == "" {
		http.Error(w, "Missing ride_fare_id", http.StatusBadRequest)
		return
	}

	faresMu.RLock()
	selectedFare, ok := faresByID[req.RideFareID]
	faresMu.RUnlock()
	if !ok {
		http.Error(w, "Unknown ride_fare_id", http.StatusBadRequest)
		return
	}

	tripID := uuid.NewString()
	tripDraft := trip{
		ID:           tripID,
		UserID:       req.UserID,
		Status:       "trip.event.created",
		SelectedFare: &selectedFare,
		Route:        selectedFare.Route,
	}

	createdTrip, err := createTripInTripService(r.Context(), tripDraft)
	if err != nil {
		http.Error(w, "Trip service unavailable", http.StatusBadGateway)
		return
	}

	if err := publishTripCreatedEvent(*createdTrip); err != nil {
		go notifyTripLifecycle(*createdTrip)
	}

	// Frontend expects {"tripID":"..."} currently.
	if err := writeJSON(w, http.StatusOK, map[string]string{"tripID": createdTrip.ID}); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		return
	}
}

func isValidCoordinate(c sharedtypes.Coordinate) bool {
	if c.Latitude < -90 || c.Latitude > 90 {
		return false
	}
	if c.Longitude < -180 || c.Longitude > 180 {
		return false
	}
	return true
}

func buildRoute(pickup, destination sharedtypes.Coordinate) sharedtypes.Route {
	distanceMeters := haversineMeters(
		pickup.Latitude,
		pickup.Longitude,
		destination.Latitude,
		destination.Longitude,
	)

	averageSpeedMetersPerSecond := 11.11
	durationSeconds := distanceMeters / averageSpeedMetersPerSecond

	midLat := (pickup.Latitude + destination.Latitude) / 2
	midLng := (pickup.Longitude + destination.Longitude) / 2

	coords := []*sharedtypes.Coordinate{
		{Latitude: pickup.Latitude, Longitude: pickup.Longitude},
		{Latitude: midLat, Longitude: midLng},
		{Latitude: destination.Latitude, Longitude: destination.Longitude},
	}

	return sharedtypes.Route{
		Geometry: []*sharedtypes.Geometry{
			{Coordinates: coords},
		},
		Duration: durationSeconds,
		Distance: distanceMeters,
	}
}

func buildRideFares(tripRoute sharedtypes.Route) []routeFare {
	baseDistanceKM := tripRoute.Distance / 1000
	expiresAt := time.Now().Add(5 * time.Minute)

	type fareMeta struct {
		slug            string
		basePrice       float64
		pricePerKM      float64
		surgeMultiplier float64
	}

	fares := []fareMeta{
		{slug: "sedan", basePrice: 2.50, pricePerKM: 1.20, surgeMultiplier: 1.00},
		{slug: "suv", basePrice: 4.00, pricePerKM: 1.80, surgeMultiplier: 1.10},
		{slug: "van", basePrice: 5.50, pricePerKM: 2.20, surgeMultiplier: 1.15},
		{slug: "luxury", basePrice: 8.00, pricePerKM: 3.50, surgeMultiplier: 1.25},
	}

	out := make([]routeFare, 0, len(fares))
	for _, f := range fares {
		total := (f.basePrice + (baseDistanceKM * f.pricePerKM)) * f.surgeMultiplier
		totalCents := math.Round(total * 100)

		out = append(out, routeFare{
			ID:                uuid.NewString(),
			PackageSlug:       f.slug,
			BasePrice:         f.basePrice,
			TotalPriceInCents: totalCents,
			ExpiresAt:         expiresAt,
			Route:             tripRoute,
		})
	}

	return out
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371000.0

	toRad := func(deg float64) float64 {
		return deg * math.Pi / 180
	}

	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}
