package resolvermaterialize

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestBuildGeneratedCandidateIdentityBytesSpanAdapters(t *testing.T) {
	const candidatesPerProtocol = 128
	const totalCandidates = 2 * candidatesPerProtocol
	if MaxGeneratedCandidateBytes%totalCandidates != 0 {
		t.Fatal("generated candidate byte fixture no longer divides the frozen cap")
	}
	bytesPerCandidate := MaxGeneratedCandidateBytes / totalCandidates
	if bytesPerCandidate+1 > MaxGeneratedCandidateBytesPerSelector {
		t.Fatal("generated candidate byte fixture exceeds the per-selector cap")
	}

	for _, testCase := range []struct {
		name      string
		extraByte bool
		wantLimit bool
	}{
		{name: "cap"},
		{name: "cap-plus-one", extraByte: true, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
			grpc := materializeTestDeclaration(ProtocolGRPC)
			thrift := materializeTestDeclaration(ProtocolThrift)
			declarations := []DeclarationInput{grpc, thrift}
			blobs := newMaterializeBlobFixture()
			rows := map[string][]store.Assertion{
				grpc.RunID:   make([]store.Assertion, 0, candidatesPerProtocol),
				thrift.RunID: make([]store.Assertion, 0, candidatesPerProtocol),
			}
			mappings := make(
				[]resolverinput.GeneratedFromMapping, 0, totalCandidates,
			)
			files := make(
				[]extract.CandidateManifestFile, 0, totalCandidates+1,
			)
			for _, fixture := range []struct {
				protocol    Protocol
				declaration DeclarationInput
				extension   string
			}{
				{ProtocolGRPC, grpc, ".proto"},
				{ProtocolThrift, thrift, ".thrift"},
			} {
				for index := range candidatesPerProtocol {
					declarationPath := fmt.Sprintf(
						"idl/%s/%03d%s", fixture.protocol, index, fixture.extension,
					)
					generatedPath := fmt.Sprintf(
						"gen/%s/%03d.go", fixture.protocol, index,
					)
					lineageBytes := bytesPerCandidate - len(declarationPath)
					if testCase.extraByte && fixture.protocol == ProtocolThrift &&
						index == candidatesPerProtocol-1 {
						lineageBytes++
					}
					if lineageBytes <= 0 {
						t.Fatal("generated candidate lineage fixture is empty")
					}
					rows[fixture.declaration.RunID] = append(
						rows[fixture.declaration.RunID],
						materializeTestAssertion(
							fixture.declaration.RunID, declarationPath,
							strings.Repeat("l", lineageBytes), index,
						),
					)
					mappings = append(mappings, resolverinput.GeneratedFromMapping{
						Protocol: string(fixture.protocol), GeneratedPath: generatedPath,
						DeclarationPath: declarationPath,
					})
					files = append(files, blobs.add(generatedPath, "package generated\n"))
				}
			}
			generated, err := json.Marshal(resolverinput.GeneratedFromSnapshot{
				Version: resolverinput.GeneratedFromSnapshotVersion, Mappings: mappings,
			})
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, blobs.add(
				resolverinput.GeneratedFromSnapshotPath, string(generated),
			))
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry,
				&materializeTestManifest{identity: testManifestDigest, files: files},
				blobs, declarations, newMaterializePublishedAssertions(rows),
			)
			prepared, err := Build(t.Context(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("dual-adapter candidate byte cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("dual-adapter candidate byte cap error = %v", err)
			}
		})
	}
}

func TestBuildDeclarationPathBytesSpanAdapters(t *testing.T) {
	const protocols = 2
	if MaxDeclarationPathBytes%(protocols*repopath.MaxBytes) != 0 {
		t.Fatal("declaration path byte fixture no longer divides the frozen cap")
	}
	pathsPerProtocol := MaxDeclarationPathBytes / (protocols * repopath.MaxBytes)

	for _, testCase := range []struct {
		name      string
		extraByte bool
		wantLimit bool
	}{
		{name: "cap"},
		{name: "cap-plus-one", extraByte: true, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
			grpc := materializeTestDeclaration(ProtocolGRPC)
			thrift := materializeTestDeclaration(ProtocolThrift)
			rows := map[string][]store.Assertion{
				grpc.RunID:   make([]store.Assertion, 0, pathsPerProtocol),
				thrift.RunID: make([]store.Assertion, 0, pathsPerProtocol+1),
			}
			for _, fixture := range []struct {
				protocol    Protocol
				declaration DeclarationInput
				extension   string
			}{
				{ProtocolGRPC, grpc, ".proto"},
				{ProtocolThrift, thrift, ".thrift"},
			} {
				for index := range pathsPerProtocol {
					prefix := fmt.Sprintf("idl/%s/%04x/", fixture.protocol, index)
					filePath := prefix + strings.Repeat(
						"p", repopath.MaxBytes-len(prefix)-len(fixture.extension),
					) + fixture.extension
					if err := repopath.Validate(filePath); err != nil {
						t.Fatalf("declaration path fixture %d: %v", index, err)
					}
					rows[fixture.declaration.RunID] = append(
						rows[fixture.declaration.RunID],
						materializeTestAssertion(
							fixture.declaration.RunID, filePath,
							fmt.Sprintf("lineage-%04x", index), index,
						),
					)
				}
			}
			if testCase.extraByte {
				rows[thrift.RunID] = append(rows[thrift.RunID],
					materializeTestAssertion(
						thrift.RunID, "z", "lineage-extra", pathsPerProtocol,
					),
				)
			}
			blobs := newMaterializeBlobFixture()
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry,
				&materializeTestManifest{identity: testManifestDigest}, blobs,
				[]DeclarationInput{grpc, thrift},
				newMaterializePublishedAssertions(rows),
			)
			prepared, err := Build(t.Context(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("dual-adapter declaration path cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("dual-adapter declaration path cap error = %v", err)
			}
		})
	}
}
