package t421

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// MaxProcessObservationNames bounds the private name-to-public-class table.
const MaxProcessObservationNames = 32

const maxPrivateProcessRefusalBytes = 512

// ProcessObservation describes completed sequential native censuses, not
// simultaneous resource bounds, executable digests or a complete process history.
// Descendant counts exclude the root; RSS includes it. A failed required sample
// makes Available false without erasing any earlier completed observation.
type ProcessObservation struct {
	MeasurementKind              string                    `json:"measurement_kind"`
	NativeHistory                string                    `json:"native_history"`
	SimultaneousBounds           string                    `json:"simultaneous_bounds"`
	Available                    bool                      `json:"available"`
	CompletedCensuses            uint64                    `json:"completed_censuses"`
	ObservedDescendants          uint64                    `json:"observed_descendants"`
	ObservedDescendantsHighWater uint64                    `json:"observed_descendants_high_water"`
	ObservedRSSBytes             uint64                    `json:"observed_rss_bytes"`
	ObservedRSSHighWaterBytes    uint64                    `json:"observed_rss_high_water_bytes"`
	Classes                      []ProcessObservationClass `json:"classes"`
	FailureClass                 string                    `json:"failure_class,omitempty"`
}

// ProcessObservationClass classifies observed kernel names only. A class name
// matching an admitted tool role does not establish that row's executable image.
type ProcessObservationClass struct {
	Class             string `json:"class"`
	ObservedRows      uint64 `json:"observed_rows"`
	ObservedHighWater uint64 `json:"observed_high_water"`
}

// ProcessObservationGauge retains bounded aggregate state, never per-PID history
// or cumulative executable-image epochs. The caller owns sample cadence and
// deadlines. Sample and Observation serialize on one mutex; a sample holds it
// across the bounded native census, with at most MaxNativeProcessRecords rows.
type ProcessObservationGauge struct {
	mu                        sync.Mutex
	rootPID                   int
	expectedRootStartIdentity string
	rootObservedName          string
	nameClasses               map[string]int
	observation               ProcessObservation
	probe                     func(context.Context, int) ([]t4013.NativeProcessRecord, error)
	privateRefusalDetail      string // First bounded private failure only; never part of ProcessObservation or receipts.
}

// NewProcessObservationGauge binds a private root lifetime and a bounded table
// from observed kernel names to closed public classes. Neither the table nor the
// root lifetime is serialized. Callers must separately bind executable images;
// the native backend does not supply their digests or complete exec histories.
func NewProcessObservationGauge(
	rootPID int,
	expectedRootStartIdentity string,
	observedNameClasses map[string]string,
) (*ProcessObservationGauge, error) {
	if expectedRootStartIdentity == "" || len(expectedRootStartIdentity) > 64 {
		return nil, errors.New("native observation scope is invalid")
	}
	gauge, err := newProcessObservationGauge(rootPID, observedNameClasses)
	if err == nil {
		gauge.expectedRootStartIdentity = expectedRootStartIdentity
	}
	return gauge, err
}

// Initial owned-root capture may fail before any start identity is available.
// Such a gauge retains unavailable evidence, never an invented binding.
func newProcessObservationGauge(rootPID int, observedNameClasses map[string]string) (*ProcessObservationGauge, error) {
	if rootPID <= 0 || len(observedNameClasses) == 0 || len(observedNameClasses) > MaxProcessObservationNames {
		return nil, errors.New("native observation scope is invalid")
	}
	classes := make([]string, 0, len(observedNameClasses))
	for name, class := range observedNameClasses {
		if !validObservedProcessName(name) || !validObservedProcessClass(class) {
			return nil, errors.New("native observation classification is invalid")
		}
		classes = append(classes, class)
	}
	slices.Sort(classes)
	classes = slices.Compact(classes)
	gauge := &ProcessObservationGauge{
		rootPID:     rootPID,
		nameClasses: make(map[string]int, len(observedNameClasses)),
		probe:       t4013.ObserveProcessTreeRecords,
		observation: ProcessObservation{
			MeasurementKind: "sampled_observation", NativeHistory: "not_established",
			SimultaneousBounds: "not_established", Classes: make([]ProcessObservationClass, len(classes)),
		},
	}
	for index, class := range classes {
		gauge.observation.Classes[index].Class = class
	}
	for name, class := range observedNameClasses {
		index, _ := slices.BinarySearch(classes, class)
		gauge.nameClasses[name] = index
	}
	return gauge, nil
}

// Sample commits only a complete valid census. Any refusal is sticky: later
// calls do not retry the native probe or replace retained positive observations.
func (gauge *ProcessObservationGauge) Sample(ctx context.Context) (ProcessObservation, error) {
	gauge.mu.Lock()
	defer gauge.mu.Unlock()
	if gauge.observation.FailureClass != "" {
		return gauge.copyObservation(), errors.New("native observation is unavailable")
	}
	if ctx == nil || gauge.probe == nil || ctx.Err() != nil {
		if ctx != nil && ctx.Err() != nil {
			return gauge.failProbe(ctx.Err())
		}
		return gauge.fail("measurement_unavailable")
	}
	rows, err := gauge.probe(ctx, gauge.rootPID)
	if err != nil || ctx.Err() != nil {
		if err == nil {
			err = ctx.Err()
		}
		return gauge.failProbe(err)
	}
	return gauge.acceptRows(rows)
}

// The caller holds mu. Startup uses the same validator for its initial native
// census, captured before the owned child can be reaped; no second scan or
// unvalidated seed is needed.
func (gauge *ProcessObservationGauge) acceptRows(rows []t4013.NativeProcessRecord) (ProcessObservation, error) {
	if len(rows) == 0 || len(rows) > t4013.MaxNativeProcessRecords || rows[0].PID != gauge.rootPID {
		return gauge.fail("invalid_census")
	}
	if rows[0].StartIdentity != gauge.expectedRootStartIdentity ||
		gauge.rootObservedName != "" && rows[0].ObservedName != gauge.rootObservedName {
		return gauge.failRow("root_identity_mismatch", rows[0])
	}
	seen := make(map[int]bool, len(rows))
	counts := make([]uint64, len(gauge.observation.Classes))
	var rss uint64
	for index, row := range rows {
		if row.PID <= 0 || seen[row.PID] || row.ParentPID < 0 || row.ParentPID == row.PID ||
			row.RSSBytes < 0 || row.StartIdentity == "" || len(row.StartIdentity) > 64 ||
			!validObservedProcessName(row.ObservedName) || index == 0 && row.RSSBytes == 0 ||
			index != 0 && !seen[row.ParentPID] {
			return gauge.failRow("invalid_census", row)
		}
		class, known := gauge.nameClasses[row.ObservedName]
		if !known {
			return gauge.failRow("unknown_classification", row)
		}
		if uint64(row.RSSBytes) > math.MaxUint64-rss {
			return gauge.fail("counter_overflow")
		}
		rss += uint64(row.RSSBytes)
		seen[row.PID] = true
		if index != 0 {
			counts[class]++
		}
	}
	if gauge.observation.CompletedCensuses == math.MaxUint64 {
		return gauge.fail("counter_overflow")
	}
	gauge.rootObservedName = rows[0].ObservedName
	gauge.observation.Available = true
	gauge.observation.CompletedCensuses++
	gauge.observation.ObservedDescendants = uint64(len(rows) - 1)
	gauge.observation.ObservedDescendantsHighWater = max(gauge.observation.ObservedDescendantsHighWater, uint64(len(rows)-1))
	gauge.observation.ObservedRSSBytes = rss
	gauge.observation.ObservedRSSHighWaterBytes = max(gauge.observation.ObservedRSSHighWaterBytes, rss)
	for index, count := range counts {
		gauge.observation.Classes[index].ObservedRows = count
		gauge.observation.Classes[index].ObservedHighWater = max(gauge.observation.Classes[index].ObservedHighWater, count)
	}
	return gauge.copyObservation(), nil
}

// Observation returns a detached source-free view. Before the first successful
// sample Available is false and CompletedCensuses is zero, not an observed zero.
func (gauge *ProcessObservationGauge) Observation() ProcessObservation {
	gauge.mu.Lock()
	defer gauge.mu.Unlock()
	return gauge.copyObservation()
}

func (gauge *ProcessObservationGauge) fail(class string) (ProcessObservation, error) {
	return gauge.failDetail(class, class)
}

func (gauge *ProcessObservationGauge) failProbe(err error) (ProcessObservation, error) {
	detail := "native census unavailable"
	if err != nil {
		detail = err.Error()
	}
	return gauge.failDetail("measurement_unavailable", detail)
}

func (gauge *ProcessObservationGauge) failRow(class string, row t4013.NativeProcessRecord) (ProcessObservation, error) {
	return gauge.failDetail(class, fmt.Sprintf("%s: PID=%d name=%q", class, row.PID, row.ObservedName[:min(len(row.ObservedName), 16)]))
}

func (gauge *ProcessObservationGauge) failDetail(class, detail string) (ProcessObservation, error) {
	gauge.observation.Available = false
	if gauge.observation.FailureClass == "" {
		gauge.observation.FailureClass = class
		gauge.privateRefusalDetail = strings.Clone(detail[:min(len(detail), maxPrivateProcessRefusalBytes)])
	}
	return gauge.copyObservation(), errors.New("native observation is unavailable")
}

func (gauge *ProcessObservationGauge) privateRefusal() string {
	gauge.mu.Lock()
	defer gauge.mu.Unlock()
	return gauge.privateRefusalDetail
}

func (gauge *ProcessObservationGauge) copyObservation() ProcessObservation {
	observation := gauge.observation
	observation.Classes = slices.Clone(observation.Classes)
	return observation
}

func validObservedProcessName(name string) bool {
	if name == "" || len(name) > 16 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, character := range name {
		if character < 33 || character > 126 {
			return false
		}
	}
	return true
}

func validObservedProcessClass(class string) bool {
	switch class {
	case "buf", "git", "go", "hdiutil", "phebs", "phebs-focused-index", "sh", "ssh-keygen", "surreal",
		"t422-author", "t422-execute", "zoekt-git-index", "compatibility", "controller":
		return true
	default:
		return false
	}
}
