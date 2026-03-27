package main

import (
	"ride-sharing/shared/types"
	"time"
)

type previewTripRequest struct {
	UserID      string           `json:"userID"`
	Pickup      types.Coordinate `json:"pickup"`
	Destination types.Coordinate `json:"destination"`
}

type previewTripResponse struct {
	Route     types.Route `json:"route"`
	RideFares []routeFare `json:"rideFares"`
}

type startTripRequest struct {
	RideFareID string `json:"rideFareID"`
	UserID     string `json:"userID"`
}

type startTripResponse struct {
	TripID string `json:"tripID"`
}

type routeFare struct {
	ID                string      `json:"id"`
	PackageSlug       string      `json:"packageSlug"`
	BasePrice         float64     `json:"basePrice"`
	TotalPriceInCents float64     `json:"totalPriceInCents"`
	ExpiresAt         time.Time   `json:"expiresAt"`
	Route             types.Route `json:"route"`
}

type trip struct {
	ID           string      `json:"id"`
	UserID       string      `json:"userID"`
	Status       string      `json:"status"`
	SelectedFare *routeFare  `json:"selectedFare,omitempty"`
	Route        types.Route `json:"route"`
}
