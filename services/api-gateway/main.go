package main

import (
	"log"
	"net/http"

	"ride-sharing/shared/env"
)

var (
	httpAddr = env.GetString("GATEWAY_HTTP_ADDR", ":8081")
)

func main() {
	log.Println("Starting API Gateway")

	amqpBus = newAMQPClient()
	if err := amqpBus.Connect(); err != nil {
		log.Printf("RabbitMQ connection failed, running without event bus: %v", err)
		amqpBus = nil
	} else {
		if err := amqpBus.StartConsumers(); err != nil {
			log.Printf("RabbitMQ consumers failed to start, running without event bus: %v", err)
			amqpBus.Close()
			amqpBus = nil
		} else {
			defer amqpBus.Close()
			log.Println("RabbitMQ connected and consumers started")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/trip/preview", handleTripPreview)
	mux.HandleFunc("/trip/start", handleTripStart)
	mux.HandleFunc("/riders", handleRidersWS)
	mux.HandleFunc("/drivers", handleDriversWS)
	mux.HandleFunc("/ws/riders", handleRidersWS)
	mux.HandleFunc("/ws/drivers", handleDriversWS)
	server := &http.Server{
		Addr:    httpAddr,
		Handler: withCORS(mux),
	}
	log.Printf("API Gateway is running on %s", httpAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("API Gateway failed to start: %v", err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
