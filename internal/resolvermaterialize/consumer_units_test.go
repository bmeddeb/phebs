package resolvermaterialize

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
)

func unitMaterializeRequest(t *testing.T) (BuildRequest, *materializeBlobFixture) {
	t.Helper()
	request, blobs := resolvedMaterializeRequest(t, t.TempDir(), newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift))
	manifest := request.Manifest.(*materializeTestManifest)
	for index, file := range manifest.files {
		switch file.Path {
		case "layout-snapshot.json":
			old := string(blobs.blobs[file.ObjectID])
			old = strings.Replace(old, `"roots":[`, `"roots":[{"kind":"source","path":"src"},`, 1)
			manifest.files[index] = blobs.add(file.Path, old)
		case "unit-snapshot.json":
			manifest.files[index] = blobs.add(file.Path, `{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"src/main.go","build_targets":["go:root"]},{"source_path":"src/main.go","start_line":7,"owners":["owner"]},{"source_path":"outside/not-admitted.go","build_targets":["go:outside"]},{"source_path":"regular.txt","build_targets":["not-a-caller-candidate"]}]}`)
		}
	}
	return request, blobs
}

func TestConsumerUnitCatalogBindsSharedOverlayProjection(t *testing.T) {
	request, blobs := unitMaterializeRequest(t)
	prepared, err := Build(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := prepared.Publish(t.Context(), func(context.Context, resolvercatalog.State) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, views, err := OpenCallerResolvers(t.Context(), request.Root, state, []Protocol{ProtocolGRPC, ProtocolThrift})
	if err != nil {
		t.Fatal(err)
	}
	manifest := request.Manifest.(*materializeTestManifest)
	var objectID string
	for _, file := range manifest.files {
		if file.Path == "src/main.go" {
			objectID = file.ObjectID
			if file.InUnit {
				t.Fatal("fixture must cover overlay outside focused local unit")
			}
		}
	}
	for _, protocol := range []Protocol{ProtocolGRPC, ProtocolThrift} {
		lookup, err := views[protocol].Resolver().ConsumerUnitsForSource("src/main.go", objectID)
		if err != nil || lookup == nil {
			t.Fatalf("lookup=%v err=%v", lookup != nil, err)
		}
		if got := lookup("src/main.go", 1); got.State != "resolved" || len(got.Candidates) != 1 || !slices.Equal(got.Candidates[0].BuildTargets, []string{"go:root"}) {
			t.Fatalf("base attribution=%+v", got)
		}
		if got := lookup("src/main.go", 7); got.State != "resolved" || !slices.Equal(got.Candidates[0].Owners, []string{"owner"}) {
			t.Fatalf("line precedence=%+v", got)
		}
		if got := lookup("outside/not-admitted.go", 1); got.State != "unavailable" || got.Reason != "no_unit_mapping" {
			t.Fatalf("outside mapping gained authority=%+v", got)
		}
		if _, err := views[protocol].Resolver().ConsumerUnitsForSource("src/main.go", strings.Repeat("f", 40)); err == nil {
			t.Fatal("crossed source OID accepted")
		}
	}
	count := 0
	for _, path := range blobs.readPaths {
		if path == "unit-snapshot.json" {
			count++
		}
		if path == "src/main.go" {
			t.Fatal("unit projection read consumer source bytes")
		}
	}
	if count != 1 {
		t.Fatalf("snapshot reads=%d want1shared across protocols", count)
	}
}

func TestConsumerUnitCatalogMissingInputIsExplicitAndMalformedStops(t *testing.T) {
	for _, test := range []struct {
		name    string
		omit    bool
		content string
	}{
		{"missing", true, ""},
		{"malformed_global_mapping", false, `{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"outside/not-admitted.go","build_targets":["duplicate","duplicate"]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, blobs := unitMaterializeRequest(t)
			manifest := request.Manifest.(*materializeTestManifest)
			for index, file := range manifest.files {
				if file.Path == "unit-snapshot.json" {
					if test.omit {
						manifest.files = append(manifest.files[:index], manifest.files[index+1:]...)
					} else {
						manifest.files[index] = blobs.add(file.Path, test.content)
					}
					break
				}
			}
			prepared, err := Build(t.Context(), request)
			if !test.omit {
				if err == nil {
					_ = prepared.Discard()
					t.Fatal("malformed global snapshot partially published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err := prepared.Publish(t.Context(), func(context.Context, resolvercatalog.State) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			_, view, err := OpenCallerResolver(t.Context(), request.Root, state, ProtocolGRPC)
			if err != nil {
				t.Fatal(err)
			}
			lookup, err := view.Resolver().ConsumerUnitsForSource("src/main.go", strings.Repeat("a", 40))
			if err != nil || lookup == nil {
				t.Fatal("missing snapshot produced missing projection")
			}
			if got := lookup("src/main.go", 1); got.State != "unavailable" || got.Reason != "no_unit_mapping" {
				t.Fatalf("missing snapshot=%+v", got)
			}
		})
	}
}

func TestConsumerUnitCatalogRejectsIncompleteOrChangedRows(t *testing.T) {
	request, _ := unitMaterializeRequest(t)
	image := buildMaterializedImage(t, request)
	var manifest resolvercatalog.Manifest
	if err := json.Unmarshal(image.manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	rows := decodeMember[consumerUnitRecord](t, image.members[ConsumerUnitMemberName])
	if rows[0].ExcludedMappings != 2 || rows[0].Reason != "outside_admitted_caller_scope" {
		t.Fatalf("excluded mappings were not explicit=%+v", rows[0])
	}
	for _, test := range []struct {
		name   string
		change func([]consumerUnitRecord) []consumerUnitRecord
		omit   bool
	}{
		{"missing_member", nil, true},
		{"missing_status", func(r []consumerUnitRecord) []consumerUnitRecord { return r[1:] }, false},
		{"duplicate_status", func(r []consumerUnitRecord) []consumerUnitRecord { return append(r, r[0]) }, false},
		{"changed_id", func(r []consumerUnitRecord) []consumerUnitRecord {
			r[1].Mapping.Candidate.ID = "au_" + strings.Repeat("0", 64)
			return r
		}, false},
		{"crossed_source_objects", func(r []consumerUnitRecord) []consumerUnitRecord {
			r[2].SourceObjectID = strings.Repeat("f", 40)
			return r
		}, false},
		{"false_missing", func(r []consumerUnitRecord) []consumerUnitRecord { r[0].State = StateUnavailable; return r }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var copyRows []consumerUnitRecord
			raw, _ := json.Marshal(rows)
			if err := json.Unmarshal(raw, &copyRows); err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				copyRows = test.change(copyRows)
			}
			root := t.TempDir()
			stage, err := resolvercatalog.NewStage(root, manifest.Identity)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range manifest.Members {
				if member.Name == ConsumerUnitMemberName && test.omit {
					continue
				}
				err = stage.AddMember(t.Context(), member.Name, member.Metadata, func(write func(json.RawMessage) error) error {
					if member.Name == ConsumerUnitMemberName {
						for _, row := range copyRows {
							if err := writeJSON(write, row); err != nil {
								return err
							}
						}
						return nil
					}
					for _, line := range strings.Split(strings.TrimSuffix(string(image.members[member.Name]), "\n"), "\n") {
						if line != "" {
							if err := write(json.RawMessage(line)); err != nil {
								return err
							}
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := stage.Seal(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			state, err := prepared.Publish(t.Context(), func(context.Context, resolvercatalog.State) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolvercatalog.Open(t.Context(), root, state); err != nil {
				t.Fatalf("generic historical custody rejected: %v", err)
			}
			if _, _, err := OpenCallerResolver(t.Context(), root, state, ProtocolGRPC); err == nil {
				t.Fatal("invalid unit member admitted by runtime")
			}
		})
	}
}

func TestConsumerUnitCatalogAggregateBoundsBeforeRetention(t *testing.T) {
	view := &consumerUnitView{encodedBytes: MaxConsumerUnitMemberBytes}
	if err := view.visit(json.RawMessage(`{}`)); !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("encoded byte cap=%v", err)
	}
	projection, err := extract.DecodeConsumerUnitSnapshot(t.Context(), `{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"src/a.go","build_targets":["x"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	mapping := projection.Mappings()[0]
	view = &consumerUnitView{status: &consumerUnitRecord{State: StateResolved}, identityBytes: extract.MaxConsumerUnitIdentityBytes}
	row := unitRecord("consumer_unit_mapping")
	row.Mapping = &mapping
	row.SourceObjectID = strings.Repeat("a", 40)
	raw, _ := json.Marshal(row)
	if err := view.visit(raw); !errors.Is(err, resolvercatalog.ErrLimit) || len(view.mappings) != 0 {
		t.Fatalf("retained identity cap=%v rows=%d", err, len(view.mappings))
	}
}
