package main

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
)

// PHEBS-36: ordinary serve defaults to the corrected typed-scope reservation
// policy, keeps the explicit rollback, and leaves the pinned T422 semantic
// launch on its config-custodied experimental switch.
func TestServeTypedScopeReservations(t *testing.T) {
	no := false
	tests := []struct {
		name     string
		cfg      config.Config
		ceremony bool
		want     bool
	}{
		{name: "ordinary default", want: true},
		{name: "ordinary rollback", cfg: config.Config{Indexing: config.Indexing{TypedScopeReservations: &no}}, want: false},
		{name: "ceremony historical", ceremony: true, want: false},
		{name: "ceremony corrected", cfg: config.Config{Experimental: config.Experimental{TypedScopeReservations: true}}, ceremony: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var launch *t422SemanticLaunch
			if tt.ceremony {
				launch = &t422SemanticLaunch{}
			}
			if got := serveTypedScopeReservations(&tt.cfg, launch); got != tt.want {
				t.Fatalf("serveTypedScopeReservations() = %v, want %v", got, tt.want)
			}
		})
	}
}
