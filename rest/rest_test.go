package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteJSONError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSONError(w, http.StatusUnauthorized, "unauthorized access", "trace-123")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "unauthorized access", resp.Error)
	assert.Equal(t, "trace-123", resp.TraceID)
}
