package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewKafkaPublisher(t *testing.T) {
	_, err := NewKafkaPublisher(KafkaConfig{})
	assert.ErrorContains(t, err, "brokers are required")

	p, err := NewKafkaPublisher(KafkaConfig{Brokers: []string{"kafka:9092"}, Username: "u", Password: "p", UseSSL: true})
	require.NoError(t, err)
	assert.Equal(t, DefaultTopic, p.w.Topic)
	assert.Equal(t, kafkago.RequireAll, p.w.RequiredAcks)
	assert.IsType(t, &kafkago.Hash{}, p.w.Balancer, "keyed: one resource's events stay in order")
	transport := p.w.Transport.(*kafkago.Transport)
	assert.NotNil(t, transport.SASL)
	assert.NotNil(t, transport.TLS)
	require.NoError(t, p.Close())

	_, err = NewKafkaPublisher(KafkaConfig{Brokers: []string{"k:9092"}, UseSSL: true, CACertPath: "/nonexistent.pem"})
	assert.ErrorContains(t, err, "kafka CA")

	bad := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(bad, []byte("not a cert"), 0o600))
	_, err = NewKafkaPublisher(KafkaConfig{Brokers: []string{"k:9092"}, UseSSL: true, CACertPath: bad})
	assert.ErrorContains(t, err, "no certificates")
}

func TestKafkaMessage(t *testing.T) {
	m := kafkaMessage(Message{Key: []byte("order/5"), Value: []byte("{}"), Headers: map[string]string{"traceparent": "00-x"}})
	assert.Equal(t, "order/5", string(m.Key))
	assert.Equal(t, []kafkago.Header{{Key: "traceparent", Value: []byte("00-x")}}, m.Headers)
}

func TestRecorderOptions(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	pub := &memPublisher{fail: 100}
	rec := newTestRecorder(t, pub,
		WithIdentityKeys("sku"),
		WithRedactKeys("salary"),
		WithLogger(zap.New(core)),
	)
	rec.Record(context.Background(), "a", Resource{},
		Before(map[string]any{"items": []map[string]any{{"sku": "A", "q": 1}}, "salary": 1, "seen": 1}),
		After(map[string]any{"items": []map[string]any{{"sku": "A", "q": 2}}, "salary": 2, "seen": 2}),
		IgnoreFields("seen"))
	closeRecorder(t, rec)

	assert.EqualValues(t, 1, rec.Failed())
	require.Equal(t, 1, logs.Len(), "the unpublished event is logged")
	assert.Equal(t, "audit: event not published", logs.All()[0].Message)

	// Rebuild to inspect what would have been sent.
	msg, err := rec.build(context.Background(), "a", Resource{}, []Option{
		Before(map[string]any{"items": []map[string]any{{"sku": "A", "q": 1}}, "salary": 1, "seen": 1}),
		After(map[string]any{"items": []map[string]any{{"sku": "A", "q": 2}}, "salary": 2, "seen": 2}),
		IgnoreFields("seen"),
	})
	require.NoError(t, err)
	assert.Contains(t, string(msg.Value), `"path":"/items/sku=A/q"`)
	assert.Contains(t, string(msg.Value), `"path":"/salary","from":"[REDACTED]","to":"[REDACTED]"`)
	assert.NotContains(t, string(msg.Value), `"path":"/seen"`)
}

func TestSharedLoggerWithoutInit(t *testing.T) {
	assert.NotPanics(t, func() { sharedLogger().Error("no logger.Init yet") })
}
