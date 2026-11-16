package safelog

import "testing"

func TestGetWithoutInit(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Get panicked before logger.Init: %v", r)
		}
	}()
	if Get() == nil {
		t.Fatal("Get returned nil")
	}
}
