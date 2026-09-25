package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/search"
)

func t422SearchWarmTestRequest(t *testing.T, epoch uint64, warm string) ([]byte, dispatchadmission.ProductionSemanticSnapshot) {
	t.Helper()
	request := t422SemanticLaunchRequest{
		Schema: t422SemanticLaunchSchema, Recipe: t422SemanticLaunchRecipe,
		PlanSHA256: "sha256:" + strings.Repeat("1", 64), ConfigSHA256: "sha256:" + strings.Repeat("2", 64),
		ServerEpoch: epoch, Repository: "local/tmp/t422-source", SearchWarm: warm,
	}
	if epoch == 3 {
		request.ReturnSourceCommit = strings.Repeat("a", 40)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	// Each epoch's initial admitted phase and producer, per t422SemanticEpochPhase.
	producer, phase := uint32(epoch+1), map[uint64]uint32{1: 2, 2: 5, 3: 6, 4: 8, 5: 12}[epoch]
	return raw, dispatchadmission.ProductionSemanticSnapshot{
		Mode: dispatchadmission.ProductionSemanticV3, InputSHA256: sha256.Sum256(raw), ProducerID: producer, Phase: phase,
	}
}

func TestT422SearchWarmLaunchRequest(t *testing.T) {
	raw, snapshot := t422SearchWarmTestRequest(t, 5, t422SearchWarmSchema)
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil || launch.request.SearchWarm != t422SearchWarmSchema {
		t.Fatalf("opted-in epoch-five launch = %+v, %v", launch, err)
	}
	if !bytes.HasSuffix(raw, []byte(`,"search_warm":"`+t422SearchWarmSchema+"\"}\n")) {
		t.Fatalf("search_warm is not the final canonical field: %s", raw)
	}
	plain, snapshot := t422SearchWarmTestRequest(t, 5, "")
	if bytes.Contains(plain, []byte("search_warm")) {
		t.Fatal("an unset opt-in changed the launch request bytes")
	}
	if _, err := decodeT422SemanticLaunch(plain, snapshot); err != nil {
		t.Fatalf("epoch five without opt-in = %v", err)
	}
	for _, test := range []struct {
		epoch uint64
		warm  string
	}{
		{1, t422SearchWarmSchema}, {2, t422SearchWarmSchema}, {3, t422SearchWarmSchema},
		{4, t422SearchWarmSchema}, {5, "t422-search-warm-v0"}, {5, " " + t422SearchWarmSchema},
	} {
		// Positive control: the same epoch without the opt-in must decode, so
		// a refusal below is the warm rule and not an unrelated admission error.
		plain, plainSnapshot := t422SearchWarmTestRequest(t, test.epoch, "")
		if _, err := decodeT422SemanticLaunch(plain, plainSnapshot); err != nil {
			t.Fatalf("epoch %d control request did not decode: %v", test.epoch, err)
		}
		raw, snapshot := t422SearchWarmTestRequest(t, test.epoch, test.warm)
		if _, err := decodeT422SemanticLaunch(raw, snapshot); err == nil {
			t.Fatalf("epoch %d warm %q was admitted", test.epoch, test.warm)
		}
	}
}

func TestT422SearchWarmRoute(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		want   bool
	}{
		{name: "bare post", want: true},
		{name: "get", mutate: func(request *http.Request) { request.Method = http.MethodGet }},
		{name: "query", mutate: func(request *http.Request) { request.URL.RawQuery = "x=1" }},
		{name: "body", mutate: func(request *http.Request) { request.ContentLength = 1 }},
		{name: "exact activation", mutate: func(request *http.Request) {
			request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
		}},
		{name: "exact ordinal", mutate: func(request *http.Request) { request.Header.Set(t421ExactReadOrdinalHeader, "1") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, t422SearchWarmPath, nil)
			if test.mutate != nil {
				test.mutate(request)
			}
			if got := t422SearchWarmRequest(request); got != test.want {
				t.Fatalf("warm request = %v, want %v", got, test.want)
			}
			// The semantic route list must admit exactly the same shape; the
			// activation/ordinal variants are exact-read shapes, not controls.
			exactShape := request.Header.Get(t421ExactReadActivationHeader) != "" || request.Header.Get(t421ExactReadOrdinalHeader) != ""
			if routed := t422SemanticRequestRoute(request); !exactShape && routed != test.want {
				t.Fatalf("semantic route = %v, want %v", routed, test.want)
			}
		})
	}
}

type t422SearchWarmFake struct {
	calls    int
	deadline time.Duration
	value    search.WholeWarmObservation
	err      error
}

func (fake *t422SearchWarmFake) WarmSelectedWholeRepository(ctx context.Context, repository string) (search.WholeWarmObservation, error) {
	fake.calls++
	if deadline, ok := ctx.Deadline(); ok {
		fake.deadline = time.Until(deadline)
	}
	value := fake.value
	if value.Repository == "" {
		value.Repository = repository
	}
	return value, fake.err
}

func TestT422SearchWarmControl(t *testing.T) {
	selected := "sha256:" + strings.Repeat("f", 64)
	newControl := func(t *testing.T, fake *t422SearchWarmFake, failures *int) *t422SearchWarmControl {
		t.Helper()
		raw, snapshot := t422SearchWarmTestRequest(t, 5, t422SearchWarmSchema)
		launch, err := decodeT422SemanticLaunch(raw, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		launch.fail = func(error) { *failures++ }
		control, err := newT422SearchWarmControl(t.Context(), launch, fake)
		if err != nil {
			t.Fatal(err)
		}
		control.isCurrent = func(context.Context) bool { return true }
		return control
	}

	t.Run("constructor refusals", func(t *testing.T) {
		raw, snapshot := t422SearchWarmTestRequest(t, 5, "")
		plain, err := decodeT422SemanticLaunch(raw, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		plain.fail = func(error) {}
		if _, err := newT422SearchWarmControl(t.Context(), plain, &t422SearchWarmFake{}); !errors.Is(err, errT422SearchWarm) {
			t.Fatalf("launch without opt-in = %v", err)
		}
		var failures int
		control := newControl(t, &t422SearchWarmFake{}, &failures)
		if _, err := newT422SearchWarmControl(t.Context(), control.launch, nil); !errors.Is(err, errT422SearchWarm) {
			t.Fatalf("nil searcher = %v", err)
		}
	})

	t.Run("warms once with source-free identities", func(t *testing.T) {
		var failures int
		fake := &t422SearchWarmFake{value: search.WholeWarmObservation{SharedValidated: true, SelectedSearchDigest: selected}}
		control := newControl(t, fake, &failures)
		recorder := httptest.NewRecorder()
		control.warm(recorder, t.Context())
		var got t422SearchWarmObservation
		decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
		decoder.DisallowUnknownFields()
		if recorder.Code != http.StatusOK || decoder.Decode(&got) != nil || fake.calls != 1 || failures != 0 ||
			got.Schema != t422SearchWarmSchema || got.Phase != 14 || !got.SharedValidated ||
			got.SelectedSearchGenerationSHA256 != selected || bytes.Contains(recorder.Body.Bytes(), []byte("local/tmp")) {
			t.Fatalf("warm = %d %s calls=%d failures=%d", recorder.Code, recorder.Body.String(), fake.calls, failures)
		}
		if fake.deadline <= 0 || fake.deadline > search.WholeGenerationWarmingTimeout {
			t.Fatalf("warm deadline = %s, want within the product warming timeout", fake.deadline)
		}
		again := httptest.NewRecorder()
		control.warm(again, t.Context())
		if again.Code != http.StatusConflict || fake.calls != 1 || failures != 1 {
			t.Fatalf("second warm = %d calls=%d failures=%d", again.Code, fake.calls, failures)
		}
	})

	for name, setup := range map[string]func(*t422SearchWarmFake, *t422SearchWarmControl){
		"warm error": func(fake *t422SearchWarmFake, _ *t422SearchWarmControl) {
			fake.err = search.ErrWholeGenerationWarming
		},
		"other repository": func(fake *t422SearchWarmFake, _ *t422SearchWarmControl) {
			fake.value.Repository = "local/tmp/other"
		},
		"no selected generation": func(fake *t422SearchWarmFake, _ *t422SearchWarmControl) {
			fake.value.SelectedSearchDigest = ""
		},
		"phase no longer current": func(_ *t422SearchWarmFake, control *t422SearchWarmControl) {
			calls := 0
			control.isCurrent = func(context.Context) bool { calls++; return calls == 1 }
		},
		"not current": func(_ *t422SearchWarmFake, control *t422SearchWarmControl) {
			control.isCurrent = func(context.Context) bool { return false }
		},
	} {
		t.Run(name, func(t *testing.T) {
			var failures int
			fake := &t422SearchWarmFake{value: search.WholeWarmObservation{SelectedSearchDigest: selected}}
			control := newControl(t, fake, &failures)
			setup(fake, control)
			recorder := httptest.NewRecorder()
			control.warm(recorder, t.Context())
			if recorder.Code != http.StatusConflict || failures != 1 {
				t.Fatalf("refusal = %d failures=%d", recorder.Code, failures)
			}
			if name == "not current" && fake.calls != 0 {
				t.Fatal("a non-current phase started a warm")
			}
		})
	}

	t.Run("unauthenticated command", func(t *testing.T) {
		var failures int
		fake := &t422SearchWarmFake{value: search.WholeWarmObservation{SelectedSearchDigest: selected}}
		control := newControl(t, fake, &failures)
		recorder := httptest.NewRecorder()
		control.command(recorder, httptest.NewRequest(http.MethodPost, t422SearchWarmPath, nil))
		if recorder.Code != http.StatusConflict || failures != 1 || fake.calls != 0 {
			t.Fatalf("unauthenticated = %d failures=%d calls=%d", recorder.Code, failures, fake.calls)
		}
	})
}
