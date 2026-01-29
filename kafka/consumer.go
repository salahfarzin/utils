package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"time"

	"github.com/salahfarzin/logger"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"go.uber.org/zap"
)

// ConsumerConfig holds configuration for the Kafka consumer.
type ConsumerConfig struct {
	Brokers            []string
	Username           string
	Password           string
	Topic              string
	GroupID            string
	UseSSL             bool
	InsecureSkipVerify bool
	CACertPath         string
}

func NewConsumer(brokers []string, topic string, groupID string, handler Handler) {
	log := logger.Get()
	log.Info("Starting Kafka consumer", zap.Strings("brokers", brokers), zap.String("topic", topic), zap.String("groupID", groupID))

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		StartOffset:    kafkago.FirstOffset,
		MinBytes:       10e3,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
		MaxWait:        500 * time.Millisecond,
		Logger:         nil,
		ErrorLogger:    nil,
	})

	go func() {
		defer reader.Close()
		RunConsumerLoop(reader, handler)
	}()
}

// NewSecureConsumer creates a Kafka consumer with SASL/SSL authentication
func NewSecureConsumer(cfg ConsumerConfig, handler Handler) {
	log := logger.Get()
	log.Info("Starting secure Kafka consumer", zap.Strings("brokers", cfg.Brokers), zap.String("topic", cfg.Topic))

	// Configure SASL mechanism
	mechanism, err := scram.Mechanism(scram.SHA512, cfg.Username, cfg.Password)
	if err != nil {
		log.Fatal("Failed to create SASL mechanism", zap.Error(err))
	}

	// Configure TLS
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	dialer := &kafkago.Dialer{
		Timeout:       10 * time.Second,
		DualStack:     true,
		SASLMechanism: mechanism,
		TLS:           tlsConfig,
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        cfg.Brokers,
		Topic:          cfg.Topic,
		GroupID:        cfg.GroupID,
		StartOffset:    kafkago.FirstOffset,
		MinBytes:       10e3,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
		MaxWait:        500 * time.Millisecond,
		Dialer:         dialer,
		Logger:         nil,
		ErrorLogger:    nil,
	})

	go func() {
		defer reader.Close()
		RunConsumerLoop(reader, handler)
	}()
}

// Sleeper interface for configurable sleep behavior
type Sleeper interface {
	Sleep(d time.Duration)
}

// DefaultSleeper implements Sleeper using time.Sleep
type DefaultSleeper struct{}

func (s *DefaultSleeper) Sleep(d time.Duration) {
	time.Sleep(d)
}

// TestSleeper implements Sleeper with instant sleep for testing
type TestSleeper struct{}

func (s *TestSleeper) Sleep(d time.Duration) {
	// No-op for tests
}

// RunConsumerLoop runs the main consumer loop with error handling
func RunConsumerLoop(reader MessageReader, handler Handler) {
	RunConsumerLoopWithSleeper(reader, handler, &DefaultSleeper{})
}

// RunConsumerLoopWithSleeper runs the main consumer loop with configurable sleep behavior
func RunConsumerLoopWithSleeper(reader MessageReader, handler Handler, sleeper Sleeper) {
	log := logger.Get()
	ctx := context.Background()
	const maxRetries = 10
	retryCount := 0
	lastErrorTime := time.Time{}

	log.Info("Kafka consumer: ready to consume messages")

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Info("Kafka consumer: context cancelled, shutting down")
				return
			}
			if errors.Is(err, io.EOF) {
				retryCount++
				if retryCount >= maxRetries {
					now := time.Now()
					if now.Sub(lastErrorTime) > 30*time.Second {
						log.Debug("Kafka consumer: no new messages, partition at end",
							zap.Int("retry_count", retryCount))
						lastErrorTime = now
					}
					sleeper.Sleep(10 * time.Second)
					retryCount = 0
				}
				continue
			}
			log.Error("Kafka consumer: failed to read message",
				zap.Error(err),
				zap.Int("retry_count", retryCount))
			retryCount++
			if retryCount >= maxRetries {
				log.Warn("Kafka consumer: max retry count reached, pausing for 10s")
				sleeper.Sleep(10 * time.Second)
				retryCount = 0
			}
			continue
		}

		retryCount = 0 // reset on success

		log.Info("Kafka consumer: received message",
			zap.String("topic", msg.Topic),
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Time("time", msg.Time))

		// Use the exported handler for testability
		if err := handler.Handle(ctx, msg.Key, msg.Value); err != nil {
			log.Error("Kafka consumer: failed to handle message", zap.Error(err))
			continue
		}
	}
}

// ConsumeMessage processes a Kafka message value using the provided handler.
// This is a helper function primarily used for testing and backward compatibility.
func ConsumeMessage(ctx context.Context, handler Handler, msgValue []byte) error {
	return handler.Handle(ctx, nil, msgValue)
}
