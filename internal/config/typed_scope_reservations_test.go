package config

import (
	"strings"
	"testing"
)

// PHEBS-36 promotes the T47.2d typed-scope reservation policy: omitted means
// on, an explicit indexing false is the rollback, and the deprecated
// experimental switch may only agree with the effective policy.
func TestTypedScopeReservationsDefaultOn(t *testing.T) {
	tests := []struct {
		name   string
		parsed string
		want   bool
	}{
		{name: "absent defaults to enabled", parsed: "{}", want: true},
		{name: "explicit true", parsed: "indexing: {typed_scope_reservations: true}", want: true},
		{name: "explicit false rolls back", parsed: "indexing: {typed_scope_reservations: false}", want: false},
		{name: "deprecated experimental true agrees", parsed: "experimental: {typed_scope_reservations: true}", want: true},
		{name: "deprecated experimental false is the old default", parsed: "experimental: {typed_scope_reservations: false}", want: true},
		{
			name:   "deprecated experimental false with explicit rollback",
			parsed: "experimental: {typed_scope_reservations: false}\nindexing: {typed_scope_reservations: false}",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.parsed))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.TypedScopeReservations(); got != tt.want {
				t.Errorf("TypedScopeReservations() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTypedScopeReservationsRejectsContradiction(t *testing.T) {
	_, err := Parse([]byte("experimental: {typed_scope_reservations: true}\nindexing: {typed_scope_reservations: false}"))
	if err == nil || !strings.Contains(err.Error(), "contradicts indexing.typed_scope_reservations") {
		t.Fatalf("Parse() error = %v, want the contradiction refusal", err)
	}
}
