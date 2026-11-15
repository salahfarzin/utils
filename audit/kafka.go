package audit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
)

// KafkaConfig configures NewKafkaPublisher. The security fields mean the same
// as in kafka.ConsumerConfig.
type KafkaConfig struct {
	Brokers []string
	// Topic defaults to DefaultTopic ("audit-events").
	Topic string

	// Username and Password enable SASL/SCRAM-SHA-512.
	Username string
	Password string
	// UseSSL enables TLS; CACertPath adds a CA to trust.
	UseSSL             bool
	InsecureSkipVerify bool
	CACertPath         string
}

// KafkaPublisher publishes audit events to Kafka.
type KafkaPublisher struct {
	w *kafkago.Writer
}

// NewKafkaPublisher returns a Publisher writing to cfg.Topic. Messages are
// partitioned by key (one resource's events stay in order) and each write
// waits for all in-sync replicas.
func NewKafkaPublisher(cfg KafkaConfig) (*KafkaPublisher, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("audit: kafka brokers are required")
	}
	if cfg.Topic == "" {
		cfg.Topic = DefaultTopic
	}
	transport := &kafkago.Transport{DialTimeout: 10 * time.Second}
	if cfg.Username != "" {
		mechanism, err := scram.Mechanism(scram.SHA512, cfg.Username, cfg.Password)
		if err != nil {
			return nil, fmt.Errorf("audit: kafka SASL: %w", err)
		}
		transport.SASL = mechanism
	}
	if cfg.UseSSL {
		tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in, as in kafka.ConsumerConfig
		if cfg.CACertPath != "" {
			pem, err := os.ReadFile(cfg.CACertPath)
			if err != nil {
				return nil, fmt.Errorf("audit: kafka CA: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("audit: kafka CA: no certificates in " + cfg.CACertPath)
			}
			tlsCfg.RootCAs = pool
		}
		transport.TLS = tlsCfg
	}
	return &KafkaPublisher{w: &kafkago.Writer{
		Addr:         kafkago.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafkago.Hash{},
		RequiredAcks: kafkago.RequireAll,
		MaxAttempts:  3,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 10 * time.Second,
		Transport:    transport,
	}}, nil
}

// Publish writes one message.
func (p *KafkaPublisher) Publish(ctx context.Context, msg Message) error {
	return p.w.WriteMessages(ctx, kafkaMessage(msg))
}

// Close flushes and closes the writer.
func (p *KafkaPublisher) Close() error { return p.w.Close() }

func kafkaMessage(msg Message) kafkago.Message {
	headers := make([]kafkago.Header, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, kafkago.Header{Key: k, Value: []byte(v)})
	}
	return kafkago.Message{Key: msg.Key, Value: msg.Value, Headers: headers}
}
