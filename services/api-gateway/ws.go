package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	sharedtypes "ride-sharing/shared/types"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	eventTripCreated        = "trip.event.created"
	eventNoDriversFound     = "trip.event.no_drivers_found"
	eventDriverAssigned     = "trip.event.driver_assigned"
	eventDriverTripRequest  = "driver.cmd.trip_request"
	eventDriverTripAccept   = "driver.cmd.trip_accept"
	eventDriverTripDecline  = "driver.cmd.trip_decline"
	eventDriverRegister     = "driver.cmd.register"
	eventDriverLocation     = "driver.cmd.location"
	eventPaymentSessionMade = "payment.event.session_created"
)

type wsOutMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type wsInMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type wsDriver struct {
	ID             string                 `json:"id"`
	Location       sharedtypes.Coordinate `json:"location"`
	Geohash        string                 `json:"geohash"`
	Name           string                 `json:"name"`
	ProfilePicture string                 `json:"profilePicture"`
	CarPlate       string                 `json:"carPlate"`
}

type riderSession struct {
	UserID string
	Conn   *websocket.Conn
	Mu     sync.Mutex
}

type driverSession struct {
	UserID      string
	PackageSlug string
	Conn        *websocket.Conn
	Mu          sync.Mutex
	Driver      wsDriver
}

type driverLocationPayload struct {
	Location sharedtypes.Coordinate `json:"location"`
	Geohash  string                 `json:"geohash"`
}

type driverDecisionPayload struct {
	TripID  string   `json:"tripID"`
	RiderID string   `json:"riderID"`
	Driver  wsDriver `json:"driver"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var (
	ridersMu sync.RWMutex
	riders   = map[string]*riderSession{}

	driversMu sync.RWMutex
	drivers   = map[string]*driverSession{}

	tripCandidatesMu sync.Mutex
	tripCandidates   = map[string][]string{}
)

func handleRidersWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.URL.Query().Get("userID")
	if userID == "" {
		http.Error(w, "Missing userID", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("rider ws upgrade failed: %v", err)
		return
	}

	session := &riderSession{
		UserID: userID,
		Conn:   conn,
	}

	ridersMu.Lock()
	riders[userID] = session
	ridersMu.Unlock()

	if err := sendDriversSnapshotToRider(userID); err != nil {
		log.Printf("send driver snapshot to rider failed: %v", err)
	}

	defer func() {
		ridersMu.Lock()
		delete(riders, userID)
		ridersMu.Unlock()
		_ = conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var in wsInMessage
		if err := json.Unmarshal(msg, &in); err != nil {
			continue
		}

		if in.Type == eventDriverLocation {
			_ = broadcastDriverLocations()
		}
	}
}

func handleDriversWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.URL.Query().Get("userID")
	packageSlug := r.URL.Query().Get("packageSlug")
	if userID == "" || packageSlug == "" {
		http.Error(w, "Missing userID or packageSlug", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("driver ws upgrade failed: %v", err)
		return
	}

	session := &driverSession{
		UserID:      userID,
		PackageSlug: packageSlug,
		Conn:        conn,
		Driver: wsDriver{
			ID: userID,
			Location: sharedtypes.Coordinate{
				Latitude:  37.7749,
				Longitude: -122.4194,
			},
			Geohash:        "",
			Name:           "Driver " + userID[:min(6, len(userID))],
			ProfilePicture: "https://randomuser.me/api/portraits/lego/1.jpg",
			CarPlate:       "TMP-" + userID[:min(4, len(userID))],
		},
	}

	driversMu.Lock()
	drivers[userID] = session
	driversMu.Unlock()

	_ = writeWS(session.Conn, &session.Mu, wsOutMessage{
		Type: eventDriverRegister,
		Data: session.Driver,
	})

	_ = broadcastDriverLocations()

	defer func() {
		driversMu.Lock()
		delete(drivers, userID)
		driversMu.Unlock()
		_ = broadcastDriverLocations()
		_ = conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var in wsInMessage
		if err := json.Unmarshal(msg, &in); err != nil {
			continue
		}

		switch in.Type {
		case eventDriverLocation:
			var payload driverLocationPayload
			if err := json.Unmarshal(in.Data, &payload); err != nil {
				continue
			}
			driversMu.Lock()
			if current, ok := drivers[userID]; ok {
				current.Driver.Location = payload.Location
				current.Driver.Geohash = payload.Geohash
			}
			driversMu.Unlock()
			_ = broadcastDriverLocations()

		case eventDriverTripAccept:
			var payload driverDecisionPayload
			if err := json.Unmarshal(in.Data, &payload); err != nil {
				continue
			}
			if err := publishDriverDecisionEvent(true, payload); err != nil {
				handleDriverDecision(true, payload)
			}

		case eventDriverTripDecline:
			var payload driverDecisionPayload
			if err := json.Unmarshal(in.Data, &payload); err != nil {
				continue
			}
			if err := publishDriverDecisionEvent(false, payload); err != nil {
				handleDriverDecision(false, payload)
			}
		}
	}
}

func notifyTripLifecycle(createdTrip trip) {
	sendTripCreatedToRider(createdTrip.UserID, createdTrip)

	selectedPackage := ""
	if createdTrip.SelectedFare != nil {
		selectedPackage = createdTrip.SelectedFare.PackageSlug
	}

	candidates := pickDriverCandidatesForPackage(selectedPackage)
	if len(candidates) == 0 {
		_ = updateTripStatusInTripService(context.Background(), createdTrip.ID, eventNoDriversFound)
		_ = sendToRider(createdTrip.UserID, wsOutMessage{
			Type: eventNoDriversFound,
			Data: map[string]any{},
		})
		return
	}

	firstCandidate := candidates[0]
	remainingCandidateIDs := make([]string, 0, len(candidates)-1)
	for _, c := range candidates[1:] {
		remainingCandidateIDs = append(remainingCandidateIDs, c.UserID)
	}
	setTripCandidates(createdTrip.ID, remainingCandidateIDs)

	_ = writeWS(firstCandidate.Conn, &firstCandidate.Mu, wsOutMessage{
		Type: eventDriverTripRequest,
		Data: createdTrip,
	})
}

func sendTripCreatedToRider(riderID string, t trip) {
	_ = sendToRider(riderID, wsOutMessage{
		Type: eventTripCreated,
		Data: t,
	})
}

func handleDriverDecision(accepted bool, payload driverDecisionPayload) {
	if payload.RiderID == "" || payload.TripID == "" {
		return
	}

	existingTrip, err := getTripFromTripService(context.Background(), payload.TripID)
	if err != nil {
		return
	}

	if !accepted {
		nextDriverID, hasNext := popNextTripCandidate(payload.TripID)
		if !hasNext {
			_ = updateTripStatusInTripService(context.Background(), payload.TripID, eventNoDriversFound)
			_ = sendToRider(payload.RiderID, wsOutMessage{
				Type: eventNoDriversFound,
				Data: map[string]any{},
			})
			clearTripCandidates(payload.TripID)
			return
		}

		nextDriver := getDriverByID(nextDriverID)
		if nextDriver == nil {
			handleDriverDecision(false, payload)
			return
		}

		_ = writeWS(nextDriver.Conn, &nextDriver.Mu, wsOutMessage{
			Type: eventDriverTripRequest,
			Data: *existingTrip,
		})
		return
	}

	clearTripCandidates(payload.TripID)
	_ = updateTripStatusInTripService(context.Background(), payload.TripID, eventDriverAssigned)

	assignedTripPayload := map[string]any{
		"id":           existingTrip.ID,
		"userID":       existingTrip.UserID,
		"status":       eventDriverAssigned,
		"selectedFare": existingTrip.SelectedFare,
		"route":        existingTrip.Route,
		"driver":       payload.Driver,
	}

	_ = sendToRider(payload.RiderID, wsOutMessage{
		Type: eventDriverAssigned,
		Data: assignedTripPayload,
	})

	amount := 0.0
	currency := "USD"
	if existingTrip.SelectedFare != nil {
		amount = existingTrip.SelectedFare.TotalPriceInCents / 100
	}

	_ = sendToRider(payload.RiderID, wsOutMessage{
		Type: eventPaymentSessionMade,
		Data: map[string]any{
			"tripID":    payload.TripID,
			"sessionID": "sess_" + uuid.NewString(),
			"amount":    amount,
			"currency":  currency,
		},
	})
}

func pickDriverForPackage(packageSlug string) *driverSession {
	driversMu.RLock()
	defer driversMu.RUnlock()

	for _, d := range drivers {
		if packageSlug == "" || d.PackageSlug == packageSlug {
			return d
		}
	}

	return nil
}

func pickDriverCandidatesForPackage(packageSlug string) []*driverSession {
	driversMu.RLock()
	defer driversMu.RUnlock()

	out := make([]*driverSession, 0, len(drivers))
	for _, d := range drivers {
		if packageSlug == "" || d.PackageSlug == packageSlug {
			out = append(out, d)
		}
	}

	return out
}

func setTripCandidates(tripID string, candidateIDs []string) {
	tripCandidatesMu.Lock()
	defer tripCandidatesMu.Unlock()
	tripCandidates[tripID] = candidateIDs
}

func popNextTripCandidate(tripID string) (string, bool) {
	tripCandidatesMu.Lock()
	defer tripCandidatesMu.Unlock()

	queue, ok := tripCandidates[tripID]
	if !ok || len(queue) == 0 {
		return "", false
	}

	next := queue[0]
	if len(queue) == 1 {
		tripCandidates[tripID] = []string{}
	} else {
		tripCandidates[tripID] = queue[1:]
	}

	return next, true
}

func clearTripCandidates(tripID string) {
	tripCandidatesMu.Lock()
	defer tripCandidatesMu.Unlock()
	delete(tripCandidates, tripID)
}

func getDriverByID(driverID string) *driverSession {
	driversMu.RLock()
	defer driversMu.RUnlock()

	driver, ok := drivers[driverID]
	if !ok {
		return nil
	}

	return driver
}

func sendDriversSnapshotToRider(riderID string) error {
	snapshot := getDriversSnapshot()
	return sendToRider(riderID, wsOutMessage{
		Type: eventDriverLocation,
		Data: snapshot,
	})
}

func broadcastDriverLocations() error {
	snapshot := getDriversSnapshot()

	ridersMu.RLock()
	defer ridersMu.RUnlock()

	for _, rider := range riders {
		_ = writeWS(rider.Conn, &rider.Mu, wsOutMessage{
			Type: eventDriverLocation,
			Data: snapshot,
		})
	}

	return nil
}

func getDriversSnapshot() []wsDriver {
	driversMu.RLock()
	defer driversMu.RUnlock()

	out := make([]wsDriver, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d.Driver)
	}
	return out
}

func sendToRider(riderID string, msg wsOutMessage) error {
	ridersMu.RLock()
	rider, ok := riders[riderID]
	ridersMu.RUnlock()
	if !ok {
		return nil
	}
	return writeWS(rider.Conn, &rider.Mu, msg)
}

func writeWS(conn *websocket.Conn, mu *sync.Mutex, msg wsOutMessage) error {
	mu.Lock()
	defer mu.Unlock()
	return conn.WriteJSON(msg)
}
