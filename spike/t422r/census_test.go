package t422r

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCensusCrossesCallerWrapperIntoExactImplementation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	commitRaw, err := gitOutput(t.Context(), root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		t.Fatal(err)
	}
	sourceCommit := strings.TrimSpace(string(commitRaw))
	records, err := Census(context.Background(), root, sourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := map[string]int{
		"/api/repo-status":                1,
		"/api/observation-progress":       14,
		"/api/extraction-progress":        10,
		"/api/caller-generation-progress": 25,
	}
	gotCounts := make(map[string]int)
	var repoStatusesBoundary, phebsPackageBoundary bool
	wantCallerSites := map[string]bool{
		"(*exactCallerMapService).generationProgress\x00caller generation changed while building the response":                 false,
		"exactCallerAuthorityConflict\x00caller map authority is no longer valid":                                              false,
		"(*exactCallerMapService).confirmWithRepository\x00caller generation changed while building the response":              false,
		"(*exactCallerMapService).confirmAuthorization\x00caller map authorization changed while building the response; retry": false,
	}
	for _, record := range records {
		if record.Endpoint == "/api/repo-status" && record.Kind == "unresolved_boundary" &&
			record.BoundaryKind == "interface_dispatch" && strings.HasSuffix(record.Boundary, ".RepoStatuses") {
			repoStatusesBoundary = true
		}
		if record.Endpoint == "/api/observation-progress" && record.BoundaryKind == "phebs_package_call" &&
			strings.HasSuffix(record.Boundary, "/observationpublication.ValidateProgress") {
			phebsPackageBoundary = true
		}
		if record.Kind != "error_site" {
			continue
		}
		gotCounts[record.Endpoint]++
		if record.Endpoint != "/api/caller-generation-progress" ||
			record.File != "internal/api/callermap_exact.go" || record.Status != 409 {
			continue
		}
		if record.DetailKind != "named_constant" {
			t.Errorf("caller progress 409 %s uses detail kind %q", record.Function, record.DetailKind)
		}
		key := record.Function + "\x00" + record.Detail
		if _, wanted := wantCallerSites[key]; wanted {
			wantCallerSites[key] = true
		}
		if record.Detail == "caller map cursor is no longer valid" {
			t.Fatal("caller-progress closure included unrelated cursor handling")
		}
	}
	for endpoint, want := range wantCounts {
		if gotCounts[endpoint] != want {
			t.Errorf("%s error sites = %d, want %d", endpoint, gotCounts[endpoint], want)
		}
	}
	for site, found := range wantCallerSites {
		if !found {
			t.Errorf("caller progress missed exact implementation site %q", site)
		}
	}
	if !repoStatusesBoundary {
		t.Error("repo-status interface boundary was silently omitted")
	}
	if !phebsPackageBoundary {
		t.Error("cross-Phebs concrete call was silently omitted")
	}
	for _, test := range []struct {
		status int
		detail string
		want   string
	}{
		{status: 500, detail: "named_constant", want: "real_fault"},
		{status: 409, detail: "inline_literal", want: "unnameable_inline"},
		{status: 409, detail: "named_constant"},
	} {
		if got := staticClassification(test.status, test.detail); got != test.want {
			t.Errorf("static classification (%d, %s) = %q, want %q", test.status, test.detail, got, test.want)
		}
	}

	var first, second bytes.Buffer
	if err := EncodeJSONL(&first, sourceCommit, records); err != nil {
		t.Fatal(err)
	}
	repeated, err := Census(context.Background(), root, sourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	if err := EncodeJSONL(&second, sourceCommit, repeated); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated JSONL encoding is not byte-identical")
	}
	if records[0].ID == recordID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", records[0]) {
		t.Fatal("site ID is not bound to the source commit")
	}
	// PHEBS-37 (2026-11-23): 37 -> 40. certificateScopes calls
	// extract.CertificateRun.Served, an infallible accessor the census
	// cannot enter, on the three progress roots that reach it.
	if !bytes.Contains(first.Bytes(), []byte(`"error_sites":50`)) ||
		!bytes.Contains(first.Bytes(), []byte(`"unresolved_boundaries":40`)) {
		header := bytes.SplitN(first.Bytes(), []byte{'\n'}, 2)[0]
		t.Fatalf("metadata header omitted exact census counts: %s", header)
	}
}
