package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/t421extractionprojection"
	t421receipt "github.com/bmeddeb/phebs/spike/t421"
)

func TestT421FinalAuthorityRepositoryRequiresOneV3Selection(t *testing.T) {
	v3 := config.ServiceCatalog{Runtime: config.ServiceCatalogRuntimeV3}
	repository, err := t421FinalAuthorityRepository(map[string]config.ServiceCatalog{
		"example.test/monorepo": v3,
	})
	if err != nil || repository != "example.test/monorepo" {
		t.Fatalf("repository = %q, %v", repository, err)
	}

	for _, test := range []struct {
		name       string
		selections map[string]config.ServiceCatalog
	}{
		{"absent", nil},
		{"multiple", map[string]config.ServiceCatalog{"one": v3, "two": v3}},
		{"default v2", map[string]config.ServiceCatalog{"one": {}}},
		{"explicit v2", map[string]config.ServiceCatalog{"one": {Runtime: config.ServiceCatalogRuntimeV2}}},
		{"unknown runtime", map[string]config.ServiceCatalog{"one": {Runtime: "v4"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if repository, err := t421FinalAuthorityRepository(test.selections); err == nil || repository != "" {
				t.Fatalf("repository = %q, %v", repository, err)
			}
		})
	}
}

func TestT421FinalAuthorityReadLimitsAreFiniteReadOnly(t *testing.T) {
	limits := t421FinalAuthorityReadLimits()
	for name, value := range map[string]uint64{
		"controls": limits.ControlFileReads,
		"store":    limits.StoreReadAttempts,
		"members":  limits.MemberVisits,
	} {
		if value == 0 || value == math.MaxUint64 {
			t.Fatalf("%s limit = %d", name, value)
		}
	}
	if limits.StoreWriteAttempts != 0 {
		t.Fatalf("write limit = %d", limits.StoreWriteAttempts)
	}
}

func TestT421FinalAuthorityMaximumReadLimitsAndRefusal(t *testing.T) {
	limits, ok := t421FinalAuthorityMaximumReadLimits()
	want := readaccounting.Counts{
		ControlFileReads: 18469, StoreReadAttempts: 528, MemberVisits: 589656064,
	}
	if !ok || limits != want || t421FinalAuthorityReadLimits() != want {
		t.Fatalf("maximum native limits = %+v, %t; want %+v", limits, ok, want)
	}

	for _, test := range []struct {
		name string
		kind readaccounting.Kind
		want readaccounting.Counts
	}{
		{
			name: "control", kind: readaccounting.ControlFileRead,
			want: readaccounting.Counts{ControlFileReads: limits.ControlFileReads + 1},
		},
		{
			name: "store", kind: readaccounting.StoreReadAttempt,
			want: readaccounting.Counts{StoreReadAttempts: limits.StoreReadAttempts + 1},
		},
		{
			name: "member", kind: readaccounting.MemberVisit,
			want: readaccounting.Counts{MemberVisits: limits.MemberVisits + 1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), limits)
			if err != nil {
				t.Fatal(err)
			}
			var limit uint64
			switch test.kind {
			case readaccounting.ControlFileRead:
				limit = limits.ControlFileReads
			case readaccounting.StoreReadAttempt:
				limit = limits.StoreReadAttempts
			case readaccounting.MemberVisit:
				limit = limits.MemberVisits
			default:
				t.Fatal("unexpected accounting kind")
			}
			if err := readaccounting.Charge(ctx, test.kind, limit); err != nil {
				t.Fatal(err)
			}
			if err := readaccounting.Charge(ctx, test.kind, 1); !errors.Is(err, readaccounting.ErrLimit) {
				t.Fatalf("over-limit charge = %v", err)
			}
			if counts, err := ledger.Finish(); counts != test.want || !errors.Is(err, readaccounting.ErrLimit) {
				t.Fatalf("refusal = %+v, %v; want %+v", counts, err, test.want)
			}
		})
	}

	sum := newT421FinalReadLimitSum()
	sum.add(math.MaxUint64 - 1)
	if !sum.valid || sum.value != math.MaxUint64-1 {
		t.Fatalf("largest representable limit = %+v", sum)
	}
	sum.add(1)
	if sum.valid {
		t.Fatal("unrepresentable refusal sentinel was admitted")
	}
	product := newT421FinalReadLimitSum()
	product.addProduct(math.MaxUint64/2+1, 2)
	if product.valid {
		t.Fatal("overflowing product was admitted")
	}
}

func TestT421FinalComponentInventoryRequiresExactComposition(t *testing.T) {
	projections := []relationshippublication.Projection{
		{Kind: "rpc", Plane: "grpc", Class: "resolved", LookupKey: "/demo.Service/Get", PostingDigest: "rpc-a"},
		{Kind: "kafka", Plane: "producer", Class: "literal", LookupKey: "demo.events", PostingDigest: "kafka-a"},
	}
	newInventory := func(t *testing.T) t421FinalComponentInventory {
		t.Helper()
		value, err := t421FinalNewComponentInventory(projections)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	matched := newInventory(t)
	if err := matched.take(t421FinalRPCComponentIdentity(rpccallerposting.Posting{
		Protocol: "grpc", Class: "resolved", LookupOperation: "/demo.Service/Get", Digest: "rpc-a",
	})); err != nil {
		t.Fatal(err)
	}
	if err := matched.take(t421FinalKafkaComponentIdentity(kafkatopicposting.Posting{
		Plane: "producer", Class: "literal", TopicSpelling: "demo.events", Digest: "kafka-a",
	})); err != nil || matched.complete() != nil {
		t.Fatalf("matched composition = %v, remaining=%d", err, len(matched))
	}

	swapped := newInventory(t)
	if err := swapped.take(t421FinalRPCComponentIdentity(rpccallerposting.Posting{
		Protocol: "grpc", Class: "resolved", LookupOperation: "/demo.Service/Get", Digest: "rpc-b",
	})); err == nil {
		t.Fatal("self-consistent replacement component was accepted")
	}
	wrongClass := newInventory(t)
	if err := wrongClass.take(t421FinalRPCComponentIdentity(rpccallerposting.Posting{
		Protocol: "grpc", Class: "unresolved", LookupOperation: "/demo.Service/Get", Digest: "rpc-a",
	})); err == nil {
		t.Fatal("component with changed class was accepted")
	}
	missing := newInventory(t)
	if err := missing.take(t421FinalRPCComponentIdentity(rpccallerposting.Posting{
		Protocol: "grpc", Class: "resolved", LookupOperation: "/demo.Service/Get", Digest: "rpc-a",
	})); err != nil || missing.complete() == nil {
		t.Fatalf("missing component = %v, remaining=%d", err, len(missing))
	}
}

func TestT421FinalHeadCommitRequiresOneEqualHEAD(t *testing.T) {
	controls := t421FinalHeadControls()
	commit, err := t421FinalHeadCommit(controls)
	if err != nil || commit != controls.Source.Revisions[0].Commit {
		t.Fatalf("commit = %q, %v", commit, err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*focusedindex.SearchGenerationControls)
	}{
		{"source absent", func(value *focusedindex.SearchGenerationControls) { value.Source.Revisions = nil }},
		{"search absent", func(value *focusedindex.SearchGenerationControls) { value.Search.Revisions = nil }},
		{"receipt absent", func(value *focusedindex.SearchGenerationControls) { value.Receipt.Revisions = nil }},
		{"source multiple", func(value *focusedindex.SearchGenerationControls) {
			value.Source.Revisions = append(value.Source.Revisions, value.Source.Revisions[0])
		}},
		{"not HEAD", func(value *focusedindex.SearchGenerationControls) {
			revision := value.Source.Revisions[0]
			revision.Selector = "release"
			value.Source.Revisions[0], value.Search.Revisions[0], value.Receipt.Revisions[0] = revision, revision, revision
		}},
		{"empty commit", func(value *focusedindex.SearchGenerationControls) {
			revision := value.Source.Revisions[0]
			revision.Commit = ""
			value.Source.Revisions[0], value.Search.Revisions[0], value.Receipt.Revisions[0] = revision, revision, revision
		}},
		{"search differs", func(value *focusedindex.SearchGenerationControls) {
			value.Search.Revisions[0].Commit = strings.Repeat("b", 40)
		}},
		{"receipt differs", func(value *focusedindex.SearchGenerationControls) {
			value.Receipt.Revisions[0].Branch = "refs/heads/other"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := t421FinalHeadControls()
			test.mutate(&invalid)
			if commit, err := t421FinalHeadCommit(invalid); err == nil || commit != "" {
				t.Fatalf("commit = %q, %v", commit, err)
			}
		})
	}
}

func TestT421FinalCommitCachesIsAtomicOnCancellation(t *testing.T) {
	newPending := func() (*t421FinalSourcePending, *t421FinalCatalogPending) {
		sourceCache := &t421FinalSourceCache{}
		catalogCache := &t421FinalCatalogCache{}
		return &t421FinalSourcePending{
				cache: sourceCache, key: t421FinalSourceKey{repository: "example.test/monorepo"},
			}, &t421FinalCatalogPending{
				cache: catalogCache, key: store.ServiceRuntimeSelector{Repository: "example.test/monorepo"},
			}
	}

	source, catalog := newPending()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := t421FinalCommitCaches(canceled, source, catalog); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit = %v", err)
	}
	if source.cache.valid || catalog.cache.valid {
		t.Fatalf("canceled commit inserted source=%t catalog=%t", source.cache.valid, catalog.cache.valid)
	}

	source, catalog = newPending()
	if err := t421FinalCommitCaches(t.Context(), source, catalog); err != nil {
		t.Fatal(err)
	}
	if !source.cache.valid || source.cache.key != source.key ||
		!catalog.cache.valid || catalog.cache.key != catalog.key {
		t.Fatalf("commit inserted source=%t catalog=%t", source.cache.valid, catalog.cache.valid)
	}
}

func TestT421FinalAuthorityJSONMatchesReceiptShape(t *testing.T) {
	t421AssertJSONShape(t, t421FinalAuthorityState{}, t421receipt.AuthorityState{},
		"physical_revision", "logical_revision")
	t421AssertJSONShape(t, t421FinalStateProjection{}, t421receipt.PhaseStateProjection{},
		"phase", "physical_revision", "logical_revision")
	t421AssertJSONShape(t, t421extractionprojection.RootResult{}, t421receipt.ExtractionRootResult{})

	response := t421FinalAuthorityResponse{
		Schema:    t421FinalAuthoritySchema,
		Authority: t421FinalAuthorityState{Current: true},
		Projection: t421FinalStateProjection{
			Schema: t421FinalProjectionSchema, ExtractionRoots: []t421extractionprojection.PhaseProjection{},
		},
		ExtractionRoots: []t421extractionprojection.RootResult{},
	}
	raw, err := t421FinalMarshal(response)
	if err != nil || !json.Valid(raw) || len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("marshal = %q, %v", raw, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	wantTopLevel := []string{"authority", "extraction_roots", "projection", "schema"}
	if got := t421SortedJSONKeys(document); !reflect.DeepEqual(got, wantTopLevel) {
		t.Fatalf("top-level keys = %v", got)
	}
	forbidden := map[string]bool{
		"repository": true, "path": true, "directory": true, "phase": true,
		"physical_revision": true, "logical_revision": true, "outcome": true,
		"error": true, "cause": true,
	}
	if key := t421ForbiddenJSONKey(document, forbidden); key != "" {
		t.Fatalf("source-free response retained %q", key)
	}
}

func t421FinalHeadControls() focusedindex.SearchGenerationControls {
	revision := store.IndexedRevision{
		Selector: "HEAD", Branch: "HEAD", Commit: strings.Repeat("a", 40),
	}
	return focusedindex.SearchGenerationControls{
		Source:  repositoryindex.SourceManifest{Revisions: []store.IndexedRevision{revision}},
		Search:  repositoryindex.SearchManifest{Revisions: []store.IndexedRevision{revision}},
		Receipt: focusedindex.SearchGenerationReceipt{Revisions: []store.IndexedRevision{revision}},
	}
}

func t421AssertJSONShape(t *testing.T, got, want any, omit ...string) {
	t.Helper()
	gotShape := t421JSONShape(reflect.TypeOf(got))
	wantShape := t421JSONShape(reflect.TypeOf(want))
	wantObject, ok := wantShape.(map[string]any)
	if !ok {
		t.Fatalf("receipt shape = %T", wantShape)
	}
	for _, name := range omit {
		delete(wantObject, name)
	}
	if !reflect.DeepEqual(gotShape, wantShape) {
		t.Fatalf("JSON shape differs:\ngot  %#v\nwant %#v", gotShape, wantShape)
	}
}

func t421JSONShape(value reflect.Type) any {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		result := make(map[string]any, value.NumField())
		for index := range value.NumField() {
			field := value.Field(index)
			if !field.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			result[name] = t421JSONShape(field.Type)
		}
		return result
	case reflect.Slice, reflect.Array:
		return []any{t421JSONShape(value.Elem())}
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	default:
		return value.Kind().String()
	}
}

func t421SortedJSONKeys(value map[string]any) []string {
	result := make([]string, 0, len(value))
	for key := range value {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func t421ForbiddenJSONKey(value any, forbidden map[string]bool) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbidden[key] {
				return key
			}
			if found := t421ForbiddenJSONKey(child, forbidden); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := t421ForbiddenJSONKey(child, forbidden); found != "" {
				return found
			}
		}
	}
	return ""
}
