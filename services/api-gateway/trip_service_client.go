package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ride-sharing/shared/env"
)

var (
	tripServiceBaseURL = env.GetString("TRIP_SERVICE_URL", "http://trip-service:8083")
	tripServiceClient  = &http.Client{Timeout: 3 * time.Second}
)

type createTripInTripServiceRequest struct {
	UserID       string     `json:"userID"`
	Status       string     `json:"status"`
	SelectedFare *routeFare `json:"selectedFare,omitempty"`
	Route        any        `json:"route"`
}

type patchTripStatusRequest struct {
	Status string `json:"status"`
}

func createTripInTripService(ctx context.Context, t trip) (*trip, error) {
	reqPayload := createTripInTripServiceRequest{
		UserID:       t.UserID,
		Status:       t.Status,
		SelectedFare: t.SelectedFare,
		Route:        t.Route,
	}

	var created trip
	if err := doTripServiceJSON(ctx, http.MethodPost, "/internal/trips", reqPayload, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func getTripFromTripService(ctx context.Context, tripID string) (*trip, error) {
	if strings.TrimSpace(tripID) == "" {
		return nil, fmt.Errorf("trip id is required")
	}

	var fetched trip
	if err := doTripServiceJSON(ctx, http.MethodGet, fmt.Sprintf("/internal/trips/%s", tripID), nil, &fetched); err != nil {
		return nil, err
	}
	return &fetched, nil
}

func updateTripStatusInTripService(ctx context.Context, tripID string, status string) error {
	if strings.TrimSpace(tripID) == "" {
		return fmt.Errorf("trip id is required")
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("status is required")
	}

	return doTripServiceJSON(
		ctx,
		http.MethodPatch,
		fmt.Sprintf("/internal/trips/%s/status", tripID),
		patchTripStatusRequest{Status: status},
		nil,
	)
}

func doTripServiceJSON(ctx context.Context, method string, path string, reqBody any, out any) error {
	url := strings.TrimRight(tripServiceBaseURL, "/") + path

	var bodyReader io.Reader
	if reqBody != nil {
		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal trip-service request: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build trip-service request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tripServiceClient.Do(req)
	if err != nil {
		return fmt.Errorf("trip-service request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trip-service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode trip-service response: %w", err)
	}

	return nil
}
