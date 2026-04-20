package infra

import (
	"fmt"
	"time"

	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
)

func SetupRabbitMQ(url string, maxRetries int, retryDelay time.Duration) (*amqp.Connection, *amqp.Channel, error) {
	var err error
	var conn *amqp.Connection
	for i := 1; i <= maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			slog.Info("Connected to RabbitMQ")
			break
		}
		slog.Warn(fmt.Sprintf("RabbitMQ not ready.. attempting to reconnect in %v", retryDelay), "attempt", i, "error", err)
		time.Sleep(retryDelay)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to open a channel: %w", err)
	}
	return conn, ch, nil
}

func DeclareWithDLQ(ch *amqp.Channel, queueName, dlqName, dlqRoutingKey, dlxExchange string) error {
	err := ch.ExchangeDeclare(dlxExchange, "direct", true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	_, err = ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    dlxExchange,
			"x-dead-letter-routing-key": dlqRoutingKey,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	_, err = ch.QueueDeclare(
		dlqName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to declare dead-letter queue: %w", err)
	}

	err = ch.QueueBind(
		dlqName,
		dlqRoutingKey,
		dlxExchange,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to bind dead-letter queue: %w", err)
	}

	return nil
}
