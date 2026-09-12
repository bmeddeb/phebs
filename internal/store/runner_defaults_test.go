package store

import "testing"

func TestRunnerDefaultAttemptConfiguration(t *testing.T) {
	for _, test := range []struct {
		name             string
		configured, want int
	}{
		{name: "native default", want: DefaultRunnerMaxAttempts},
		{name: "explicit one", configured: 1, want: 1},
		{name: "explicit override", configured: 7, want: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Pure defaults with a supplied owner string: no Hostname,
			// store, claim, timer, worker, registration or native child.
			runner := Runner{Who: "default-test", MaxAttempts: test.configured}
			runner.defaults()
			if runner.MaxAttempts != test.want || DefaultRunnerMaxAttempts != 3 {
				t.Fatal("runner default or explicit override changed", runner.MaxAttempts)
			}
		})
	}
}
