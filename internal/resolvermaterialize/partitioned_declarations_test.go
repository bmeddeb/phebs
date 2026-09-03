package resolvermaterialize

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func partitionedMaterializeFixture(t *testing.T, count int) (BuildRequest, *materializeAssertionReader) {
	t.Helper()
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	declaration := materializeTestDeclaration(ProtocolGRPC)
	declaration.AuthoritySchema = store.PartitionedExtractionDomainSchema
	declaration.PlanDigest = workerTestDigest('1')
	declaration.RootDigest = workerTestDigest('2')
	declaration.GenerationDigest = declaration.RootDigest
	declaration.CandidatePolicyDigest = workerTestDigest('3')
	authority := store.PartitionedAssertionAuthority{
		Repository: testRepository, Domain: declaration.Domain, RunID: declaration.RunID,
		PlanDigest: declaration.PlanDigest, RootDigest: declaration.RootDigest,
		Commit: testCommit, CandidateManifestDigest: testManifestDigest,
		CandidatePolicyDigest: declaration.CandidatePolicyDigest,
	}
	rows := make([]store.Assertion, count)
	for index := range rows {
		rows[index] = materializeTestAssertion(declaration.RunID,
			fmt.Sprintf("idl/%05d.proto", index), fmt.Sprintf("lineage-%05d", index), index)
	}
	reader := &materializeAssertionReader{
		rows: map[string][]store.Assertion{declaration.RunID: rows},
		runs: map[string]materializeAssertionRun{declaration.RunID: {
			status: "staged", sealed: true, authority: authority,
		}},
		current: map[string]store.PartitionedAssertionAuthority{declaration.Domain: authority},
	}
	request := newMaterializeBuildRequest(t, t.TempDir(), registry,
		&materializeTestManifest{identity: testManifestDigest}, newMaterializeBlobFixture(),
		[]DeclarationInput{declaration}, reader)
	return request, reader
}

func TestDeclarationReaderDispatchesBySelectedRunKind(t *testing.T) {
	for _, test := range []struct {
		name         string
		legacy       bool
		status       string
		sealed       bool
		active       bool
		publishedKey bool
		quarantined  bool
		unrooted     bool
		superseded   bool
		empty        bool
		wantRows     int
		wantErr      error
	}{
		{name: "partitioned staged sealed current", status: "staged", sealed: true, wantRows: 1},
		{name: "partitioned empty stays empty", status: "staged", sealed: true, empty: true},
		{name: "partitioned unsealed refuses", status: "staged", wantErr: store.ErrNotFound},
		{name: "partitioned active refuses", status: "staged", sealed: true, active: true, wantErr: store.ErrNotFound},
		{name: "partitioned legacy key refuses", status: "staged", sealed: true, publishedKey: true, wantErr: store.ErrNotFound},
		{name: "partitioned quarantine refuses", status: "staged", sealed: true, quarantined: true, wantErr: store.ErrNotFound},
		{name: "partitioned unrooted refuses", status: "staged", sealed: true, unrooted: true, wantErr: store.ErrNotFound},
		{name: "partitioned superseded refuses", status: "staged", sealed: true, superseded: true, wantErr: store.ErrNotFound},
		{name: "partitioned published is not native", status: "published", sealed: true, wantErr: store.ErrNotFound},
		{name: "legacy published", legacy: true, status: "published", publishedKey: true, wantRows: 1},
		{name: "legacy status alone is not publication", legacy: true, status: "published"},
		{name: "legacy empty cannot fall back", legacy: true, status: "staged", sealed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, reader := partitionedMaterializeFixture(t, 1)
			input := request.Declarations[0]
			run := reader.runs[input.RunID]
			run.status, run.sealed = test.status, test.sealed
			run.active, run.publishedKey, run.quarantined = test.active, test.publishedKey, test.quarantined
			if test.unrooted {
				run.authority.RootDigest = ""
			}
			reader.runs[input.RunID] = run
			if test.superseded {
				current := reader.current[input.Domain]
				current.RootDigest = workerTestDigest('4')
				reader.current[input.Domain] = current
			}
			if test.empty {
				reader.rows[input.RunID] = nil
			}
			if test.legacy {
				input = materializeTestDeclaration(ProtocolGRPC)
			}
			writes := 0
			_, err := emitDeclarationTargets(t.Context(), request, request.Registry.adapters[0],
				&input, &materializationBudget{}, func(json.RawMessage) error { writes++; return nil })
			if !errors.Is(err, test.wantErr) || writes != test.wantRows {
				t.Fatalf("declaration result = %d rows, %v; want %d, %v", writes, err, test.wantRows, test.wantErr)
			}
			if test.legacy {
				if reader.legacyCalls != 1 || len(reader.partitionedCalls) != 0 {
					t.Fatalf("legacy dispatch = %d / %d", reader.legacyCalls, len(reader.partitionedCalls))
				}
			} else if reader.legacyCalls != 0 || len(reader.partitionedCalls) != 1 {
				t.Fatalf("partitioned dispatch = %d / %d", reader.legacyCalls, len(reader.partitionedCalls))
			}
		})
	}
}

func TestPartitionedDeclarationPaginationReassertsExactAuthority(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		t.Run(fmt.Sprintf("supersede=%t", supersede), func(t *testing.T) {
			request, reader := partitionedMaterializeFixture(t, assertionPageSize+1)
			input := request.Declarations[0]
			wanted := reader.current[input.Domain]
			reader.partitionedHook = func(query store.AssertionQuery) {
				if supersede && query.After != nil {
					current := reader.current[input.Domain]
					current.RootDigest = workerTestDigest('4')
					reader.current[input.Domain] = current
				}
			}
			prepared, err := Build(t.Context(), request)
			if supersede {
				if !errors.Is(err, store.ErrConflict) || prepared != nil {
					t.Fatalf("superseded build = %v, %v", prepared, err)
				}
				entries, readErr := os.ReadDir(request.Root)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("failed build retained partial stage: %v, %v", entries, readErr)
				}
			} else {
				if err != nil || prepared == nil {
					t.Fatalf("stable build = %v, %v", prepared, err)
				}
				if err := prepared.Discard(); err != nil {
					t.Fatal(err)
				}
			}
			if reader.legacyCalls != 0 || len(reader.partitionedCalls) != 2 {
				t.Fatalf("bounded partitioned reads = %d, legacy = %d", len(reader.partitionedCalls), reader.legacyCalls)
			}
			for index, authority := range reader.partitionedCalls {
				query := reader.calls[index]
				if authority != wanted || query.RunID != wanted.RunID || query.Repo != wanted.Repository ||
					query.Limit != assertionPageSize || !query.AllowTruncate || query.Predicate != "DECLARES_OPERATION" {
					t.Fatalf("page %d escaped exact bounded declaration scope: %+v %+v", index, authority, query)
				}
			}
		})
	}
}

func TestPartitionedDeclarationReaderNeverFallsBackWithoutCapability(t *testing.T) {
	request, reader := partitionedMaterializeFixture(t, 1)
	request.Assertions = struct{ AssertionReader }{reader}
	input := request.Declarations[0]
	_, err := emitDeclarationTargets(t.Context(), request, request.Registry.adapters[0],
		&input, &materializationBudget{}, func(json.RawMessage) error { return nil })
	if err == nil || len(reader.calls) != 0 {
		t.Fatalf("missing capability fell through to legacy: %v, calls=%d", err, len(reader.calls))
	}
	input.AuthoritySchema = "unknown-partitioned-schema"
	request.Assertions = reader
	_, err = emitDeclarationTargets(t.Context(), request, request.Registry.adapters[0],
		&input, &materializationBudget{}, func(json.RawMessage) error { return nil })
	if err == nil || len(reader.calls) != 0 {
		t.Fatalf("unknown schema dispatched a read: %v, calls=%d", err, len(reader.calls))
	}
}
