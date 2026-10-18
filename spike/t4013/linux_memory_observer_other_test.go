//go:build !linux

package t4013

import (
	"errors"
	"reflect"
	"testing"
)

func TestLinuxMemoryObserverNonLinuxRefuses(t *testing.T) {
	got, err := ObserveLinuxMemory(t.Context())
	if !errors.Is(err, ErrLinuxMemoryObservation) || !reflect.DeepEqual(got, LinuxMemoryObservation{}) {
		t.Fatalf("non-Linux observer = %+v, %v", got, err)
	}
}
