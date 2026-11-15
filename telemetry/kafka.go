package telemetry

import (
	"context"
	"strconv"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// InjectKafka adds ctx's trace context (traceparent, baggage) to the
// message headers, so the consumer continues the producer's trace.
// utils/kafka's Producer does this for you.
func InjectKafka(ctx context.Context, msg *kafkago.Message) {
	otel.GetTextMapPropagator().Inject(ctx, kafkaCarrier{msg})
}

// StartKafkaConsumer starts a consumer span for msg, continuing the trace in
// its headers, and returns a context with that span and a trace-aware
// logger. End the span with EndSpan. utils/kafka's consumer loop does this
// for you.
func StartKafkaConsumer(ctx context.Context, msg kafkago.Message) (context.Context, trace.Span) {
	ctx = otel.GetTextMapPropagator().Extract(ctx, kafkaCarrier{&msg})
	ctx, span := otel.Tracer(ScopeName).Start(ctx, msg.Topic+" process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation.type", "process"),
			attribute.String("messaging.destination.name", msg.Topic),
			attribute.Int("messaging.destination.partition.id", msg.Partition),
			attribute.String("messaging.kafka.offset", strconv.FormatInt(msg.Offset, 10)),
		))
	return WithTraceLogger(ctx), span
}

// EndSpan records err on span (if any) and ends it.
func EndSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// kafkaCarrier adapts message headers to propagation.TextMapCarrier.
type kafkaCarrier struct{ msg *kafkago.Message }

func (c kafkaCarrier) Get(key string) string {
	for _, h := range c.msg.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c kafkaCarrier) Set(key, value string) {
	for i, h := range c.msg.Headers {
		if h.Key == key {
			c.msg.Headers[i].Value = []byte(value)
			return
		}
	}
	c.msg.Headers = append(c.msg.Headers, kafkago.Header{Key: key, Value: []byte(value)})
}

func (c kafkaCarrier) Keys() []string {
	keys := make([]string, len(c.msg.Headers))
	for i, h := range c.msg.Headers {
		keys[i] = h.Key
	}
	return keys
}
