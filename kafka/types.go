package kafka

import (
	"context"

	kafkago "github.com/segmentio/kafka-go"
)

type MessageReader interface {
	ReadMessage(ctx context.Context) (kafkago.Message, error)
	Close() error
}

type Handler interface {
	Handle(ctx context.Context, key, value []byte) error
}
