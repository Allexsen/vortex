package worker

import (
	"errors"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrPermanent = errors.New("permanent error: do not retry")
	ErrTransient = errors.New("transient error: requeue task")
)

func HandleError(err error, msg amqp.Delivery) {
	if err == nil {
		return
	}

	if errors.Is(err, ErrTransient) {
		log.Printf("[TRANSIENT] Requeuing task. Reason: %v", err)
		msg.Nack(false, true)
		return
	}

	// Fallback for ErrPermanent or completely unmapped errors
	log.Printf("[PERMANENT/UNKNOWN] Dropping task. Reason: %v", err)
	msg.Ack(false)
}
