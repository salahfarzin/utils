package testutils

import (
	"testing"

	"github.com/salahfarzin/logger"
)

// InitLogger initializes the logger for testing purposes.
func InitLogger(t *testing.T) {
	logger.Init()
}
