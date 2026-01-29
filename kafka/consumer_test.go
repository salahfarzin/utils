package kafka_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	kafkaPkg "github.com/salahfarzin/utils/kafka"
	"github.com/salahfarzin/utils/testutils"
	kafka "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMessageReader is a mock implementation of MessageReader
type MockMessageReader struct {
	mock.Mock
	callCount int
}

func (m *MockMessageReader) ReadMessage(ctx context.Context) (kafka.Message, error) {
	m.callCount++
	args := m.Called(ctx)
	return args.Get(0).(kafka.Message), args.Error(1)
}

func (m *MockMessageReader) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockHandler is a mock implementation of kafkaPkg.Handler
type MockHandler struct {
	mock.Mock
}

func (m *MockHandler) Handle(ctx context.Context, key, value []byte) error {
	args := m.Called(ctx, key, value)
	return args.Error(0)
}

func TestConsumeMessage(t *testing.T) {
	testutils.InitLogger(t)

	tests := []struct {
		name        string
		msgKey      []byte
		msgValue    []byte
		setupMock   func(*MockHandler)
		expectError bool
		validate    func(*testing.T, *MockHandler)
	}{
		{
			name:     "Valid message",
			msgKey:   []byte("test-key"),
			msgValue: []byte("test-value"),
			setupMock: func(m *MockHandler) {
				m.On("Handle", mock.Anything, mock.Anything, []byte("test-value")).Return(nil)
			},
			expectError: false,
			validate: func(t *testing.T, m *MockHandler) {
				m.AssertExpectations(t)
			},
		},
		{
			name:     "Handler returns error",
			msgKey:   []byte("test-key"),
			msgValue: []byte("test-value"),
			setupMock: func(m *MockHandler) {
				m.On("Handle", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("handler error"))
			},
			expectError: true,
			validate: func(t *testing.T, m *MockHandler) {
				m.AssertExpectations(t)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &MockHandler{}
			tt.setupMock(mockHandler)

			err := kafkaPkg.ConsumeMessage(context.Background(), mockHandler, tt.msgValue)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			tt.validate(t, mockHandler)
		})
	}
}

func TestNewConsumer(t *testing.T) {
	testutils.InitLogger(t)

	t.Run("NewConsumer initializes without panic", func(t *testing.T) {
		mockHandler := &MockHandler{}

		assert.NotPanics(t, func() {
			kafkaPkg.NewConsumer([]string{"localhost:9092"}, "test-topic", "test-group", mockHandler)
		})
	})
}

func TestRunConsumerLoop(t *testing.T) {
	testutils.InitLogger(t)

	t.Run("Successful message processing", func(t *testing.T) {
		mockReader := &MockMessageReader{}
		mockHandler := &MockHandler{}

		msg := kafka.Message{
			Topic:     "test-topic",
			Partition: 0,
			Offset:    1,
			Key:       []byte("key"),
			Value:     []byte("value"),
			Time:      time.Now(),
		}

		mockReader.On("ReadMessage", mock.Anything).Return(msg, nil).Once()
		mockReader.On("ReadMessage", mock.Anything).Return(kafka.Message{}, context.Canceled).Once()
		mockHandler.On("Handle", mock.Anything, []byte("key"), []byte("value")).Return(nil)

		kafkaPkg.RunConsumerLoopWithSleeper(mockReader, mockHandler, &kafkaPkg.TestSleeper{})

		mockReader.AssertExpectations(t)
		mockHandler.AssertExpectations(t)
	})

	t.Run("Context canceled error", func(t *testing.T) {
		mockReader := &MockMessageReader{}
		mockHandler := &MockHandler{}

		mockReader.On("ReadMessage", mock.Anything).Return(kafka.Message{}, context.Canceled)

		kafkaPkg.RunConsumerLoopWithSleeper(mockReader, mockHandler, &kafkaPkg.TestSleeper{})

		mockReader.AssertExpectations(t)
		mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("EOF error with retry logic", func(t *testing.T) {
		mockReader := &MockMessageReader{}
		mockHandler := &MockHandler{}

		mockReader.On("ReadMessage", mock.Anything).Return(kafka.Message{}, io.EOF).Times(11)
		mockReader.On("ReadMessage", mock.Anything).Return(kafka.Message{}, context.Canceled).Once()

		kafkaPkg.RunConsumerLoopWithSleeper(mockReader, mockHandler, &kafkaPkg.TestSleeper{})

		mockReader.AssertExpectations(t)
		mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything, mock.Anything)
	})
}
