package store

import (
	"errors"
	"strings"
	"testing"
)

// The staging THROWs are deterministic rule violations whose text would
// otherwise match the transient "conflict" substring and be retried 64 times.
func TestPermanentMarkerIsNeverRetryable(t *testing.T) {
	for _, msg := range []string{
		"phebs-permanent: duplicate semantic assertion has conflicting attributes",
		"phebs-permanent: assertion evidence cannot both support and contradict",
		"phebs-permanent: evidence run exceeds a bounded storage limit",
		"PHEBS-PERMANENT: with a unique index and a retry hint",
	} {
		err := errors.New("An error occurred: " + msg)
		if isRetryable(err) {
			t.Errorf("isRetryable(%q) = true, want false", msg)
		}
		if isRetryableEnqueue(err) {
			t.Errorf("isRetryableEnqueue(%q) = true, want false", msg)
		}
	}
	if !isRetryable(errors.New("read-write conflict, please retry")) {
		t.Error("transient conflict stopped being retryable")
	}
	if !isRetryableEnqueue(errors.New("unique index violation")) {
		t.Error("unique-index enqueue race stopped being retryable")
	}
}

// Every deterministic THROW in the staging SQL must carry the marker; a new
// THROW without it silently reintroduces the retry storm.
func TestAddEvidenceThrowsAllCarryPermanentMarker(t *testing.T) {
	for _, stmt := range strings.Split(addEvidenceSQL, "THROW ")[1:] {
		if !strings.HasPrefix(stmt, "'"+permanentErrMarker) {
			t.Errorf("THROW without %q marker: %.60s", permanentErrMarker, stmt)
		}
	}
}
