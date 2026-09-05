package t421

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func observationFixture(t *testing.T) (*ProcessObservationGauge, []t4013.NativeProcessRecord) {
	t.Helper()
	gauge, err := NewProcessObservationGauge(41, "private-start-token", map[string]string{
		"private-root": "controller", "renamed-root": "controller", "git": "git",
		"git-upload-pack": "git", "surreal": "surreal",
	})
	if err != nil {
		t.Fatal(err)
	}
	return gauge, []t4013.NativeProcessRecord{
		{PID: 41, ParentPID: 1, RSSBytes: 4096, StartIdentity: "private-start-token", ObservedName: "private-root"},
		{PID: 42, ParentPID: 41, RSSBytes: 8192, StartIdentity: "private-child-token", ObservedName: "git"},
	}
}

func TestProcessObservationConstruction(t *testing.T) {
	for _, test := range []struct {
		name  string
		root  int
		token string
		names map[string]string
	}{
		{"root", 0, "token", map[string]string{"phebs": "phebs"}},
		{"identity", 1, "", map[string]string{"phebs": "phebs"}},
		{"long identity", 1, strings.Repeat("x", 65), map[string]string{"phebs": "phebs"}},
		{"empty names", 1, "token", nil},
		{"empty name", 1, "token", map[string]string{"": "phebs"}},
		{"long name", 1, "token", map[string]string{strings.Repeat("x", 17): "phebs"}},
		{"path name", 1, "token", map[string]string{"/private/phebs": "phebs"}},
		{"control name", 1, "token", map[string]string{"phebs\n": "phebs"}},
		{"unknown class", 1, "token", map[string]string{"phebs": "private-project"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProcessObservationGauge(test.root, test.token, test.names); err == nil {
				t.Fatal("invalid native observation binding accepted")
			}
		})
	}
	names := make(map[string]string)
	for index := range MaxProcessObservationNames {
		names["name"+strconv.Itoa(index)] = "git"
	}
	gauge, err := NewProcessObservationGauge(1, "token", names)
	if err != nil || len(gauge.nameClasses) != MaxProcessObservationNames {
		t.Fatalf("maximum name inventory: %v", err)
	}
	names["overflow"] = "git"
	if _, err := NewProcessObservationGauge(1, "token", names); err == nil {
		t.Fatal("name inventory overflow accepted")
	}
	if len(gauge.nameClasses) != MaxProcessObservationNames {
		t.Fatal("gauge retained mutable caller name map")
	}
}

func TestProcessObservationCompletedCensusHighWaterAndSourceFree(t *testing.T) {
	gauge, rows := observationFixture(t)
	gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
	initial := gauge.Observation()
	if initial.Available || initial.CompletedCensuses != 0 || initial.FailureClass != "" ||
		initial.MeasurementKind != "sampled_observation" || initial.NativeHistory != "not_established" ||
		initial.SimultaneousBounds != "not_established" {
		t.Fatalf("initial observation misstates availability: %+v", initial)
	}
	rows[1].RSSBytes = 1 << 40 // A positive overshoot must not be clipped to a refusal threshold.
	first, err := gauge.Sample(t.Context())
	if err != nil || !first.Available || first.CompletedCensuses != 1 || first.ObservedDescendants != 1 ||
		first.ObservedRSSBytes != 1<<40+4096 || first.ObservedRSSHighWaterBytes != first.ObservedRSSBytes ||
		!reflect.DeepEqual(first.Classes, []ProcessObservationClass{
			{Class: "controller"}, {Class: "git", ObservedRows: 1, ObservedHighWater: 1}, {Class: "surreal"},
		}) {
		t.Fatalf("first observation = %+v, %v", first, err)
	}
	first.Classes[1].Class = "caller-mutation"
	rows = rows[:1]
	second, err := gauge.Sample(t.Context())
	if err != nil || second.CompletedCensuses != 2 || second.ObservedDescendants != 0 ||
		second.ObservedDescendantsHighWater != 1 || second.ObservedRSSBytes != 4096 ||
		second.ObservedRSSHighWaterBytes != 1<<40+4096 || second.Classes[1].Class != "git" ||
		second.Classes[1].ObservedRows != 0 || second.Classes[1].ObservedHighWater != 1 {
		t.Fatalf("second observation = %+v, %v", second, err)
	}
	raw, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private", "start_identity", "observed_name", "pid", "executable", "sha256"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("source-bearing or unsupported identity field %q: %s", private, raw)
		}
	}
	if len(raw) > 4096 {
		t.Fatalf("bounded observation unexpectedly large: %d", len(raw))
	}
}

func TestProcessObservationFailuresAreStickyAndPreservePositiveSamples(t *testing.T) {
	for _, test := range []struct {
		name   string
		class  string
		mutate func(*ProcessObservationGauge, *[]t4013.NativeProcessRecord)
	}{
		{"probe denial", "measurement_unavailable", func(g *ProcessObservationGauge, _ *[]t4013.NativeProcessRecord) {
			g.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
				return nil, errors.New("EPERM PID 9876 /private/sensitive/source")
			}
		}},
		{"empty", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { *rows = nil }},
		{"truncated", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			(*rows)[1].StartIdentity = ""
		}},
		{"oversized", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			*rows = make([]t4013.NativeProcessRecord, t4013.MaxNativeProcessRecords+1)
		}},
		{"wrong root", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { (*rows)[0].PID++ }},
		{"root lifetime", "root_identity_mismatch", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			(*rows)[0].StartIdentity = "replacement-private-token"
		}},
		{"root name drift", "root_identity_mismatch", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			(*rows)[0].ObservedName = "renamed-root"
		}},
		{"unknown name", "unknown_classification", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			(*rows)[1].ObservedName = "private-unknown"
		}},
		{"duplicate PID", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { (*rows)[1].PID = 41 }},
		{"missing parent", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { (*rows)[1].ParentPID = 99 }},
		{"negative RSS", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { (*rows)[1].RSSBytes = -1 }},
		{"zero root RSS", "invalid_census", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) { (*rows)[0].RSSBytes = 0 }},
		{"RSS overflow", "counter_overflow", func(_ *ProcessObservationGauge, rows *[]t4013.NativeProcessRecord) {
			(*rows)[0].RSSBytes, (*rows)[1].RSSBytes = math.MaxInt64, math.MaxInt64
			*rows = append(*rows, t4013.NativeProcessRecord{
				PID: 43, ParentPID: 41, RSSBytes: math.MaxInt64, StartIdentity: "third", ObservedName: "git",
			})
		}},
		{"census counter overflow", "counter_overflow", func(g *ProcessObservationGauge, _ *[]t4013.NativeProcessRecord) {
			g.observation.CompletedCensuses = math.MaxUint64
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gauge, rows := observationFixture(t)
			gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
			if _, err := gauge.Sample(t.Context()); err != nil {
				t.Fatal(err)
			}
			test.mutate(gauge, &rows)
			want := gauge.Observation()
			want.Available, want.FailureClass = false, test.class
			got, err := gauge.Sample(t.Context())
			if err == nil || !reflect.DeepEqual(got, want) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "9876") {
				t.Fatalf("failed census = %+v, %v; want %+v", got, err, want)
			}
			gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
				t.Fatal("sticky failure retried native observation")
				return nil, nil
			}
			if again, err := gauge.Sample(t.Context()); err == nil || !reflect.DeepEqual(again, want) {
				t.Fatalf("sticky census = %+v, %v", again, err)
			}
		})
	}
}

func TestProcessObservationCanceledAndNilContexts(t *testing.T) {
	for _, test := range []string{"canceled", "nil", "canceled during probe"} {
		t.Run(test, func(t *testing.T) {
			gauge, rows := observationFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
				if test != "canceled during probe" {
					t.Fatal("invalid context reached native probe")
				}
				cancel()
				return rows, nil
			}
			switch test {
			case "canceled":
				cancel()
			case "nil":
				ctx = nil
			}
			got, err := gauge.Sample(ctx)
			if err == nil || got.Available || got.CompletedCensuses != 0 || got.FailureClass != "measurement_unavailable" {
				t.Fatalf("invalid context observation = %+v, %v", got, err)
			}
		})
	}
}

func TestProcessObservationHasNoCumulativeImageEpochLimit(t *testing.T) {
	gauge, rows := observationFixture(t)
	gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
	for index := range 9000 {
		// A reused child PID and recognized command-name changes establish no
		// complete history and must not recreate T40's cumulative lifetime cap.
		rows[1].StartIdentity = strconv.Itoa(index)
		rows[1].ObservedName = []string{"git", "git-upload-pack", "surreal"}[index%3]
		rows[1].RSSBytes = 0
		if got, err := gauge.Sample(t.Context()); err != nil || got.CompletedCensuses != uint64(index+1) {
			t.Fatalf("observation %d = %+v, %v", index, got, err)
		}
	}
	for len(rows) < t4013.MaxNativeProcessRecords {
		rows = append(rows, t4013.NativeProcessRecord{
			PID: 41 + len(rows), ParentPID: 41, RSSBytes: 1, StartIdentity: "child", ObservedName: "git",
		})
	}
	if got, err := gauge.Sample(t.Context()); err != nil || got.ObservedDescendants != uint64(t4013.MaxNativeProcessRecords-1) {
		t.Fatalf("maximum census = %+v, %v", got, err)
	}
}

func TestProcessObservationConcurrentDetachedViews(t *testing.T) {
	gauge, rows := observationFixture(t)
	gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 100 {
				view := gauge.Observation()
				view.Classes[0].Class = "caller-mutation"
			}
		})
	}
	for range 100 {
		if got, err := gauge.Sample(t.Context()); err != nil || got.Classes[0].Class != "controller" {
			t.Fatalf("concurrent observation = %+v, %v", got, err)
		}
	}
	readers.Wait()
}

func TestProcessObservationRealNativeRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native process collector is Darwin-only")
	}
	rows, err := t4013.ObserveProcessTreeRecords(t.Context(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	gauge, err := NewProcessObservationGauge(os.Getpid(), rows[0].StartIdentity, map[string]string{
		rows[0].ObservedName: "controller",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := gauge.Sample(t.Context())
	if err != nil || !got.Available || got.CompletedCensuses != 1 || got.ObservedRSSHighWaterBytes == 0 {
		t.Fatalf("real native observation = %+v, %v", got, err)
	}
}
