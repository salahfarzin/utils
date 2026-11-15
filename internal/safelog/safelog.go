// Package safelog gives library code a logger without the risk of
// logger.Get(), which panics until the service has called logger.Init.
package safelog

import (
	"github.com/salahfarzin/logger"
	"go.uber.org/zap"
)

// Get returns the service's logger once logger.Init has run, and zap's
// production logger (JSON on stderr) before that. It never panics.
func Get() (l *zap.Logger) {
	defer func() {
		if recover() != nil {
			if l, _ = zap.NewProduction(); l == nil {
				l = zap.NewNop()
			}
		}
	}()
	return logger.Get()
}
