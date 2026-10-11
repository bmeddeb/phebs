package gocaller_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/spike/t201"
)

func TestSourceOutcomesCaptureDecisionsWithoutChangingFacts(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	corpus := memoryCorpus{repo: "synthetic.invalid/mono", commit: strings.Repeat("b", 40), files: cloneFiles(profile.Files), attribution: frozenAttribution(profile)}
	corpus.files["bad.go"] = []byte("package broken; func (")
	corpus.files["invalid.go"] = []byte{0xff}
	corpus.files["large.go"] = []byte(strings.Repeat(" ", 4<<20+1))
	var plain, captured []sdk.Fact
	coverage, err := gocaller.NewGRPC().Extract(t.Context(), corpus, func(f sdk.Fact) error { plain = append(plain, f); return nil })
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]sdk.SourceOutcome{}
	ctx := sdk.WithSourceOutcomes(t.Context(), func(o sdk.SourceOutcome) error {
		if _, ok := outcomes[o.Path]; ok {
			t.Fatalf("duplicate outcome %s", o.Path)
		}
		outcomes[o.Path] = o
		return nil
	})
	got, err := gocaller.NewGRPC().Extract(ctx, corpus, func(f sdk.Fact) error { captured = append(captured, f); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, captured) || !reflect.DeepEqual(coverage, got) {
		t.Fatal("capture changed evidence or coverage")
	}
	expected := 0
	for path := range corpus.files {
		if strings.HasSuffix(path, ".go") {
			expected++
		}
	}
	if len(outcomes) != expected {
		t.Fatalf("got %d source outcomes, want %d; missing=%v", len(outcomes), expected, missingSourceOutcomes(corpus.files, outcomes))
	}
	for path, state := range map[string]string{"bad.go": "failed", "invalid.go": "excluded", "large.go": "excluded"} {
		if outcomes[path].State != state {
			t.Fatalf("%s = %+v", path, outcomes[path])
		}
	}
	analyzed := 0
	for _, o := range outcomes {
		if o.State == "analyzed" {
			analyzed++
		}
	}
	if analyzed == 0 {
		t.Fatal("no completed scans retained")
	}
	outcomes = map[string]sdk.SourceOutcome{}
	if _, err := gocaller.NewGRPC().Extract(ctx, domainTypedCorpus{corpus}, func(sdk.Fact) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 {
		t.Fatal("read-only member partition invented processing outcomes")
	}
	outcomes = map[string]sdk.SourceOutcome{}
	corpus.files["index.scip"] = []byte("not SCIP")
	if _, err := gocaller.NewGRPC().Extract(ctx, corpus, func(sdk.Fact) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if o.State == "analyzed" {
			t.Fatal("malformed typed input reported complete analysis")
		}
	}
}

func TestSourceOutcomesNoMethodsAndSinkFailure(t *testing.T) {
	corpus := memoryCorpus{repo: "example.invalid/empty", commit: strings.Repeat("c", 40), files: map[string][]byte{"empty.go": []byte("package empty\n")}}
	var outcome sdk.SourceOutcome
	ctx := sdk.WithSourceOutcomes(t.Context(), func(o sdk.SourceOutcome) error { outcome = o; return nil })
	if _, err := gocaller.NewGRPC().Extract(ctx, corpus, func(sdk.Fact) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if outcome.State != "excluded" || outcome.Reason != "no_generated_methods" {
		t.Fatalf("read without scan = %+v", outcome)
	}
	sentinel := errors.New("receipt write failed")
	ctx = sdk.WithSourceOutcomes(t.Context(), func(sdk.SourceOutcome) error { return sentinel })
	if _, err := gocaller.NewGRPC().Extract(ctx, corpus, func(sdk.Fact) error { return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("sink failure = %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := gocaller.NewGRPC().Extract(ctx, corpus, func(sdk.Fact) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func missingSourceOutcomes(files map[string][]byte, outcomes map[string]sdk.SourceOutcome) []string {
	var missing []string
	for path := range files {
		if strings.HasSuffix(path, ".go") {
			if _, ok := outcomes[path]; !ok {
				missing = append(missing, path)
			}
		}
	}
	return missing
}
