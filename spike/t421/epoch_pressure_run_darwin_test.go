//go:build darwin

package t421

import (
	"context"
	"testing"
)

func TestExecutionPressureRefusesUnownedVolume(t *testing.T) {
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {flow: &ExecutionEpochOne{}}} {
		for _, volume := range []*executionPressureVolume{nil, {}} {
			if run.Pressure(context.Background(), volume) == nil {
				t.Fatal("unowned pressure run admitted")
			}
		}
	}
}
