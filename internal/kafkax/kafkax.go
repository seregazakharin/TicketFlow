package kafkax

import (
	"context"
	"encoding/json"
	"time"

	"github.com/segmentio/kafka-go"

	"ticketflow/internal/config"
)

type Event struct {
	Type      string    `json:"type"`
	OrderID   string    `json:"order_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	EventID   string    `json:"event_id,omitempty"`
	Quantity  int       `json:"quantity,omitempty"`
	Status    string    `json:"status,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func Writer(topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:         kafka.TCP(config.KafkaBrokers()...),
		Topic:        topic,
		RequiredAcks: kafka.RequireOne,
		Async:        false,
		Balancer:     &kafka.LeastBytes{},
	}
}

func Publish(ctx context.Context, writer *kafka.Writer, event Event) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.OrderID),
		Value: payload,
		Time:  event.CreatedAt,
	})
}
