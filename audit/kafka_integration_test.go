package audit

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/salahfarzin/utils/middlewares"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Runs against a real broker when AUDIT_KAFKA_BROKERS is set, e.g.
//
//	docker run -d --name kafka -p 9092:9092 apache/kafka:3.9.0
//	AUDIT_KAFKA_BROKERS=localhost:9092 go test ./audit -run Integration
func TestKafkaPublisherIntegration(t *testing.T) {
	brokers := os.Getenv("AUDIT_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("AUDIT_KAFKA_BROKERS not set")
	}
	topic := "audit-events-it-" + time.Now().Format("150405.000000")
	conn, err := kafkago.Dial("tcp", strings.Split(brokers, ",")[0])
	require.NoError(t, err)
	require.NoError(t, conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 3, ReplicationFactor: 1}))
	require.NoError(t, conn.Close())

	otel.SetTextMapPropagator(propagation.TraceContext{})
	pub, err := NewKafkaPublisher(KafkaConfig{Brokers: strings.Split(brokers, ","), Topic: topic})
	require.NoError(t, err)
	rec, err := New("it", pub, WithLogger(zap.NewNop()))
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), middlewares.UserKey, &middlewares.User{Uuid: "u-1"})
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	}))
	require.NoError(t, rec.RecordSync(ctx, "order.update", Resource{Type: "order", ID: "5"},
		Before(map[string]int{"q": 1}), After(map[string]int{"q": 2})))
	rec.Record(ctx, "order.update", Resource{Type: "order", ID: "5"},
		Before(map[string]int{"q": 2}), After(map[string]int{"q": 3}))
	closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, rec.Close(closeCtx))

	reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: strings.Split(brokers, ","), Topic: topic, GroupID: topic})
	defer reader.Close()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRead()

	var partition int
	for i := range 2 {
		m, err := reader.ReadMessage(readCtx)
		require.NoError(t, err)
		assert.Equal(t, "order/5", string(m.Key))
		if i == 0 {
			partition = m.Partition
		}
		assert.Equal(t, partition, m.Partition, "one resource, one partition: its events stay in order")
		headers := map[string]string{}
		for _, h := range m.Headers {
			headers[h.Key] = string(h.Value)
		}
		assert.Equal(t, "00-01000000000000000000000000000000-0200000000000000-01", headers["traceparent"])
		var ev Event
		require.NoError(t, json.Unmarshal(m.Value, &ev))
		assert.Equal(t, "u-1", ev.Actor.ID)
		assert.Equal(t, []Change{{Op: OpReplace, Path: "/q", From: float64(i + 1), To: float64(i + 2)}}, ev.Changes)
	}
}
