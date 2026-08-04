package diagnostics

import "testing"

func TestGuardContainsDiagnosticSinkPanic(t *testing.T) {
	guard(func() { panic("diagnostic sink") })
}
