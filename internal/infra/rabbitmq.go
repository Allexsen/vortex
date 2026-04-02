package infra

import (
	"time"

	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
)

func SetupRabbitMQ(url string) (*amqp.Connection, *amqp.Channel, error) {
	var err error
	var conn *amqp.Connection
	for i := 1; i <= 3; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			slog.Info("Connected to RabbitMQ")
			break
		}
		slog.Warn("RabbitMQ not ready.. attempting to reconnect in 5s", "attempt", i, "error", err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		slog.Error("Failed to connect to RabbitMQ after 3 attempts", "error", err)
		return nil, nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		slog.Error("Failed to open a channel", "error", err)
		return nil, nil, err
	}
	return conn, ch, nil
}
