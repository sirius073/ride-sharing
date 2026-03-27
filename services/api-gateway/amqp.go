package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"ride-sharing/shared/contracts"
	"ride-sharing/shared/env"

	amqp "github.com/rabbitmq/amqp091-go"
)

var amqpBus *amqpClient

type amqpClient struct {
	url      string
	exchange string

	mu      sync.Mutex
	conn    *amqp.Connection
	channel *amqp.Channel
}

func newAMQPClient() *amqpClient {
	return &amqpClient{
		url:      env.GetString("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/"),
		exchange: env.GetString("RABBITMQ_EXCHANGE", "ride-sharing.events"),
	}
}

func (c *amqpClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open rabbitmq channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		c.exchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("declare exchange: %w", err)
	}

	c.conn = conn
	c.channel = ch
	return nil
}

func (c *amqpClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.channel != nil {
		_ = c.channel.Close()
		c.channel = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *amqpClient) PublishJSON(ctx context.Context, routingKey string, payload any) error {
	c.mu.Lock()
	ch := c.channel
	exchange := c.exchange
	c.mu.Unlock()

	if ch == nil {
		return fmt.Errorf("rabbitmq channel is not initialized")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal amqp payload: %w", err)
	}

	msg := contracts.AmqpMessage{
		OwnerID: "api-gateway",
		Data:    body,
	}
	wrappedBody, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal amqp envelope: %w", err)
	}

	return ch.PublishWithContext(
		ctx,
		exchange,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        wrappedBody,
		},
	)
}

func (c *amqpClient) StartConsumers() error {
	if err := c.consumeRoutingKey(
		"gateway.trip.created",
		[]string{contracts.TripEventCreated},
		func(raw []byte) error {
			var t trip
			if err := json.Unmarshal(raw, &t); err != nil {
				return err
			}
			notifyTripLifecycle(t)
			return nil
		},
	); err != nil {
		return err
	}

	if err := c.consumeRoutingKey(
		"gateway.driver.accept",
		[]string{contracts.DriverCmdTripAccept},
		func(raw []byte) error {
			var payload driverDecisionPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			handleDriverDecision(true, payload)
			return nil
		},
	); err != nil {
		return err
	}

	if err := c.consumeRoutingKey(
		"gateway.driver.decline",
		[]string{contracts.DriverCmdTripDecline},
		func(raw []byte) error {
			var payload driverDecisionPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			handleDriverDecision(false, payload)
			return nil
		},
	); err != nil {
		return err
	}

	return nil
}

func (c *amqpClient) consumeRoutingKey(queueName string, routingKeys []string, handler func(raw []byte) error) error {
	c.mu.Lock()
	ch := c.channel
	exchange := c.exchange
	c.mu.Unlock()

	if ch == nil {
		return fmt.Errorf("rabbitmq channel is not initialized")
	}

	q, err := ch.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("declare queue %s: %w", queueName, err)
	}

	for _, key := range routingKeys {
		if err := ch.QueueBind(q.Name, key, exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %s with key %s: %w", queueName, key, err)
		}
	}

	deliveries, err := ch.Consume(
		q.Name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume queue %s: %w", queueName, err)
	}

	go func() {
		for d := range deliveries {
			var wrapped contracts.AmqpMessage
			if err := json.Unmarshal(d.Body, &wrapped); err != nil {
				log.Printf("amqp: invalid message envelope on %s: %v", queueName, err)
				_ = d.Ack(false)
				continue
			}

			if err := handler(wrapped.Data); err != nil {
				log.Printf("amqp: handler error on %s: %v", queueName, err)
				_ = d.Nack(false, false)
				continue
			}

			_ = d.Ack(false)
		}
	}()

	return nil
}

func publishTripCreatedEvent(t trip) error {
	if amqpBus == nil {
		return fmt.Errorf("amqp bus is not initialized")
	}
	return amqpBus.PublishJSON(context.Background(), contracts.TripEventCreated, t)
}

func publishDriverDecisionEvent(accepted bool, payload driverDecisionPayload) error {
	if amqpBus == nil {
		return fmt.Errorf("amqp bus is not initialized")
	}

	routingKey := contracts.DriverCmdTripDecline
	if accepted {
		routingKey = contracts.DriverCmdTripAccept
	}
	return amqpBus.PublishJSON(context.Background(), routingKey, payload)
}
