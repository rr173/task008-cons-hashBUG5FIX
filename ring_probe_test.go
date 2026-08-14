package main

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

// TestWriteErrorExtractsWrappedStatus verifies that writeError correctly
// extracts the HTTP status code from an error that has been wrapped.
func TestWriteErrorExtractsWrappedStatus(t *testing.T) {
	original := badRequest("validation failed")
	wrapped := fmt.Errorf("additional context: %w", original)

	rec := httptest.NewRecorder()
	writeError(rec, wrapped)

	if rec.Code != 400 {
		t.Fatalf("expected status 400 for wrapped badRequest error, got %d", rec.Code)
	}
}
