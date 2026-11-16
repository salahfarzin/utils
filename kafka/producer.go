package kafka

import (
	"context"

	kafkago "github.com/segmentio/kafka-go"
)

// ProducerConfig holds configuration for the Kafka producer.
type ProducerConfig struct {
	Brokers []string
	Topic   string
}

// Producer wraps kafkago.Writer for producing events.
type Producer struct {
	Writer *kafkago.Writer
}

// NewProducer creates a new Kafka producer.
func NewProducer(cfg ProducerConfig) *Producer {
	return &Producer{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(cfg.Brokers...),
			Topic:                  cfg.Topic,
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
	}
}

// Produce sends a raw message to Kafka.
func (p *Producer) Produce(ctx context.Context, key, value []byte) error {
	msg := kafkago.Message{
		Key:   key,
		Value: value,
	}
	return p.Writer.WriteMessages(ctx, msg)
}

// Close closes the underlying Kafka writer.
func (p *Producer) Close() error {
	return p.Writer.Close()
}
