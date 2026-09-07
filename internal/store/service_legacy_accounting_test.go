//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// legacyServiceRows is one persisted v2 catalog with its planned first
// service-state transition, encoded exactly as the legacy writers would store
// it. The rows come from the production planners, not hand-written digests.
type legacyServiceRows struct {
	publication servicecatalog.Publication
	source      string
	pointer     serviceCatalogCurrentRec
	generation  serviceCatalogGenerationRec
	version     serviceCatalogAuthorityVersionRec
	summary     servicecatalog.RepositoryState
	summaryRow  map[string]any
	stateRow    map[string]any
}

func legacyServiceAccountingRows(t *testing.T) legacyServiceRows {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	publication := serviceStateV2Publication(t, "example.com/acme/legacy-accounting", strings.Repeat("1", 40))
	publication.ControlRevision, publication.PublishedAt = 1, now
	source, err := servicecatalog.SourceGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	projections, err := servicecatalog.ProjectServices(publication)
	if err != nil {
		t.Fatal(err)
	}
	updates, states, tombstones, err := planServiceStateTransition(
		publication, projections, map[string]servicecatalog.ServiceState{}, nil, now,
	)
	if err != nil || len(updates) != 1 {
		t.Fatalf("planned %d legacy updates: %v", len(updates), err)
	}
	summary, err := buildServiceStateSummary(publication, projections, states, tombstones, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	return legacyServiceRows{
		publication: publication, source: source,
		pointer: serviceCatalogCurrentRec{
			Repository: publication.Repository, GenerationDigest: publication.GenerationDigest,
			ControlRevision: 1, PublishedAt: now,
		},
		generation: serviceCatalogGenerationRec{
			Schema: publication.Schema, Repository: publication.Repository,
			SourceKind: publication.SourceKind, SourcePath: publication.SourcePath,
			SourceCommit: publication.SourceCommit, SourceCensusDigest: publication.SourceCensusDigest,
			SourceFileCount: publication.SourceFileCount, AcceptedFileCount: publication.AcceptedFileCount,
			UnownedFileCount: publication.UnownedFileCount,
			AuthorityKind:    publication.Authority.Kind, AuthorityID: publication.Authority.ID,
			AuthorityVersion: publication.Authority.Version, CatalogDigest: publication.CatalogDigest,
			LegacyAnalysisUnitDigest: publication.LegacyAnalysisUnitDigest,
			GenerationDigest:         publication.GenerationDigest,
			CatalogJSON:              string(publication.Canonical), RecordedAt: now,
		},
		version: serviceCatalogAuthorityVersionRec{
			Repository: publication.Repository, AuthorityKind: publication.Authority.Kind,
			AuthorityID: publication.Authority.ID, AuthorityVersion: publication.Authority.Version,
			CatalogDigest: publication.CatalogDigest, RecordedAt: now,
		},
		summary:    summary,
		summaryRow: serviceRepositoryStateContent(summary),
		stateRow:   serviceStateContent(updates[0].State),
	}
}

// catalogReplies scripts the verified-catalog opening shared by every legacy
// service reader: current pointer, immutable generation, then the
// authority-version claim.
func (rows legacyServiceRows) catalogReplies() []legacyAccountingReply {
	return []legacyAccountingReply{
		{"SELECT * FROM $rid", []serviceCatalogCurrentRec{rows.pointer},
			map[string]any{"rid": serviceCatalogCurrentID(rows.publication.Repository)}},
		{"SELECT * FROM $rid", []serviceCatalogGenerationRec{rows.generation},
			map[string]any{"rid": serviceCatalogGenerationID(rows.publication.GenerationDigest)}},
		{"SELECT * FROM $rid", []serviceCatalogAuthorityVersionRec{rows.version},
			map[string]any{"rid": serviceCatalogAuthorityVersionID(rows.publication)}},
	}
}

func (rows legacyServiceRows) pointerReply() legacyAccountingReply {
	return legacyAccountingReply{
		"SELECT generation_digest, control_revision FROM $rid", []serviceCatalogCurrentRec{rows.pointer},
		map[string]any{"rid": serviceCatalogCurrentID(rows.publication.Repository)},
	}
}

func (rows legacyServiceRows) summaryReply(present bool) legacyAccountingReply {
	summary := []map[string]any{}
	if present {
		summary = append(summary, rows.summaryRow)
	}
	return legacyAccountingReply{
		"SELECT * FROM $rid", summary,
		map[string]any{"rid": serviceRepositoryStateID(rows.publication.Repository)},
	}
}

type legacyServiceAccountingRun struct {
	ctx        context.Context
	store      *Surreal
	owner      *storeCallOwner
	controller *storeaccounting.Controller
	native     *storeSDKTestConnection
	err        error
}

func runLegacyServiceAccounting(
	t *testing.T,
	selected bool,
	replies []legacyAccountingReply,
	call func(context.Context, *Surreal) error,
) legacyServiceAccountingRun {
	t.Helper()
	run := legacyServiceAccountingRun{ctx: t.Context()}
	if selected {
		run.ctx, run.owner, run.controller = storeAccountingFixture(t, 40, 2)
	}
	db, native := storeAccountingDB(t, run.ctx, run.owner)
	legacyAccountingScript(t, native, run.controller, replies)
	run.store, run.native = &Surreal{db: db, accounting: run.owner}, native
	run.err = call(run.ctx, run.store)
	if selected && run.err == nil {
		if err := run.controller.Fence(); err != nil {
			t.Fatal(err)
		}
		if err := run.owner.checkpoint(run.ctx); err != nil {
			t.Fatalf("legacy service read left an active typed SDK call: %v", err)
		}
	}
	return run
}

func TestServiceLegacyAccountingReads(t *testing.T) {
	rows := legacyServiceAccountingRows(t)
	repository := rows.publication.Repository
	search := "sha256:" + strings.Repeat("5", 64)
	stateReply := legacyAccountingReply{
		"SELECT * FROM $rid", []map[string]any{rows.stateRow},
		map[string]any{"rid": serviceStateID(repository, "orders")},
	}
	for _, test := range []struct {
		name    string
		call    func(context.Context, *Surreal) error
		replies []legacyAccountingReply
		want    error
	}{
		{"catalog", func(ctx context.Context, s *Surreal) error {
			stored, err := s.GetServiceCatalog(ctx, repository)
			if err == nil && (stored.GenerationDigest != rows.publication.GenerationDigest || stored.ControlRevision != 1) {
				return errors.New("verified catalog changed")
			}
			return err
		}, rows.catalogReplies(), nil},
		{"catalog_absent", func(ctx context.Context, s *Surreal) error {
			_, err := s.GetServiceCatalog(ctx, repository)
			return err
		}, []legacyAccountingReply{
			{"SELECT * FROM $rid", []serviceCatalogCurrentRec{}, map[string]any{"rid": serviceCatalogCurrentID(repository)}},
		}, ErrNotFound},
		{"generation", func(ctx context.Context, s *Surreal) error {
			stored, err := s.GetServiceCatalogGeneration(ctx, repository, rows.publication.GenerationDigest)
			if err == nil && (stored.GenerationDigest != rows.publication.GenerationDigest || stored.ControlRevision != 0) {
				return errors.New("historical generation changed")
			}
			return err
		}, rows.catalogReplies()[1:], nil},
		{"generation_absent", func(ctx context.Context, s *Surreal) error {
			_, err := s.GetServiceCatalogGeneration(ctx, repository, rows.publication.GenerationDigest)
			return err
		}, []legacyAccountingReply{
			{"SELECT * FROM $rid", []serviceCatalogGenerationRec{},
				map[string]any{"rid": serviceCatalogGenerationID(rows.publication.GenerationDigest)}},
		}, ErrInvalidServiceCatalogPublication},
		{"summary", func(ctx context.Context, s *Surreal) error {
			summary, err := s.GetServiceStateSummary(ctx, repository)
			if err == nil && summary.SummaryDigest != rows.summary.SummaryDigest {
				return errors.New("summary point changed")
			}
			return err
		}, []legacyAccountingReply{rows.pointerReply(), rows.summaryReply(true)}, nil},
		{"summary_absent", func(ctx context.Context, s *Surreal) error {
			_, err := s.GetServiceStateSummary(ctx, repository)
			return err
		}, []legacyAccountingReply{rows.pointerReply(), rows.summaryReply(false)}, ErrNotFound},
		{"confirm", func(ctx context.Context, s *Surreal) error {
			return s.ConfirmServiceStateSnapshot(ctx, repository, rows.summary)
		}, []legacyAccountingReply{rows.pointerReply(), rows.summaryReply(true)}, nil},
		{"read", func(ctx context.Context, s *Surreal) error {
			read, err := s.GetServiceStateRead(ctx, repository, "orders")
			if err == nil && (read.Entry.State.ServiceKey != "orders" || read.Entry.Projection == nil) {
				return errors.New("service state read changed")
			}
			return err
		}, append(append(rows.catalogReplies(), rows.summaryReply(true)), stateReply), nil},
		{"accepted", func(ctx context.Context, s *Surreal) error {
			snapshot, err := s.GetAcceptedServiceStateSnapshot(ctx, repository, 8)
			if err == nil && len(snapshot.States) != 1 {
				return errors.New("accepted snapshot changed")
			}
			return err
		}, append(append(rows.catalogReplies(), rows.summaryReply(true),
			legacyAccountingReply{"AND disposition = $accepted", []map[string]any{rows.stateRow},
				map[string]any{"repository": repository, "limit": 9}}),
			rows.pointerReply(), rows.summaryReply(true)), nil},
		{"list", func(ctx context.Context, s *Surreal) error {
			page, err := s.ListServiceStates(ctx, repository, ServiceStateFilter{}, ServiceStatePosition{}, 10)
			if err == nil && (len(page.Entries) != 1 || page.Continuation != nil) {
				return errors.New("service state page changed")
			}
			return err
		}, append(rows.catalogReplies(), rows.summaryReply(true),
			legacyAccountingReply{"AND service_key > $after", []map[string]any{rows.stateRow},
				map[string]any{"repository": repository, "after": "", "limit": maxServiceStateScanPage + 1}}), nil},
		{"activation", func(ctx context.Context, s *Surreal) error {
			needed, err := s.ServiceGenerationActivationNeeded(
				ctx, repository, rows.publication.GenerationDigest, rows.source, search,
			)
			if err == nil && needed {
				return errors.New("empty activation census reported work")
			}
			return err
		}, append(rows.catalogReplies(), rows.summaryReply(true),
			legacyAccountingReply{"(status != $current OR (active_search_generation ?? '') != $search)", []serviceStateRec{},
				map[string]any{"repository": repository, "search": search}}), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					run := runLegacyServiceAccounting(t, selected, test.replies, test.call)
					if !errors.Is(run.err, test.want) || run.native.calls != len(test.replies) {
						t.Fatalf("calls=%d error=%v; want %d/%v", run.native.calls, run.err, len(test.replies), test.want)
					}
					if !selected {
						return
					}
					prefix, err := run.controller.Snapshot()
					if err != nil || prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Producers[0].Calls != 0 {
						t.Fatalf("legacy service reads invented accounting: %+v %v", prefix, err)
					}
				})
			}
		})
	}
}

// The two legacy v2 writers keep their exact ordinary transactions and refuse
// in selected mode before native write submission, after their accounted
// preflight reads, with a private diagnostic naming the site.
func TestServiceLegacyAccountingUnsupportedWriters(t *testing.T) {
	rows := legacyServiceAccountingRows(t)
	repository := rows.publication.Repository
	for _, test := range []struct {
		name      string
		site      string
		sql       string
		call      func(context.Context, *Surreal) error
		preflight []legacyAccountingReply
		write     []legacyAccountingReply
	}{
		{"catalog", "PublishServiceCatalog", publishServiceCatalogSQL, func(ctx context.Context, s *Surreal) error {
			return s.PublishServiceCatalog(ctx, rows.publication)
		}, nil, append([]legacyAccountingReply{
			{publishServiceCatalogSQL, []serviceCatalogCurrentRec{rows.pointer}, map[string]any{
				"repo_rid":       repoID(repository),
				"current_rid":    serviceCatalogCurrentID(repository),
				"generation_rid": serviceCatalogGenerationID(rows.publication.GenerationDigest),
				"version_rid":    serviceCatalogAuthorityVersionID(rows.publication),
				"catalog_json":   string(rows.publication.Canonical),
			}},
		}, rows.catalogReplies()...)},
		{"states", "commitServiceStateTransition", reconcileServiceStatesSQL, func(ctx context.Context, s *Surreal) error {
			return s.ReconcileServiceStates(ctx, rows.publication)
		}, []legacyAccountingReply{
			rows.pointerReply(),
			rows.summaryReply(false),
			{"WHERE repository = $repository AND removed = false LIMIT $limit", []serviceStateRec{},
				map[string]any{"repository": repository, "limit": servicecatalog.MaxServices + 1}},
			{"SELECT * FROM $rids", []serviceStateRec{},
				map[string]any{"rids": []models.RecordID{serviceStateID(repository, "orders")}}},
		}, []legacyAccountingReply{
			{reconcileServiceStatesSQL, []map[string]any{rows.summaryRow}, map[string]any{
				"catalog_rid":               serviceCatalogCurrentID(repository),
				"summary_rid":               serviceRepositoryStateID(repository),
				"catalog_generation":        rows.publication.GenerationDigest,
				"expected_summary_revision": 0,
			}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					replies := slices.Clone(test.preflight)
					if !selected {
						replies = append(replies, test.write...)
					}
					run := runLegacyServiceAccounting(t, selected, replies, test.call)
					if run.native.calls != len(replies) {
						t.Fatalf("calls=%d error=%v; want %d", run.native.calls, run.err, len(replies))
					}
					if !selected {
						if run.err != nil {
							t.Fatalf("ordinary legacy service writer changed: %v", run.err)
						}
						return
					}
					if !errors.Is(run.err, storeaccounting.ErrDescriptor) {
						t.Fatalf("legacy service writer was not refused: %v", run.err)
					}
					// Failure delivery may already have reached the parent; only the
					// retained prefix matters here, not the controller's own error.
					prefix, _ := run.controller.Snapshot()
					if prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Complete {
						t.Fatalf("refusal invented a write prefix: %+v", prefix)
					}
					diagnostic, ok := run.owner.PrivateRefusal()
					if !ok || diagnostic.Method != "query" || diagnostic.SQLPrefix != test.sql[:min(len(test.sql), 120)] {
						t.Fatalf("declared refusal lost its private diagnostic: %+v", diagnostic)
					}
					named := false
					frames := runtime.CallersFrames(diagnostic.Callers[:])
					for {
						frame, more := frames.Next()
						named = named || strings.Contains(frame.Function, "(*Surreal)."+test.site)
						if !more {
							break
						}
					}
					if !named {
						t.Fatalf("private diagnostic does not name %s", test.site)
					}
					before := run.native.calls
					if _, err := run.store.GetServiceCatalog(run.ctx, repository); err == nil || run.native.calls != before {
						t.Fatal("legacy service writer refusal did not latch later reads")
					}
				})
			}
		})
	}
}

func TestServiceLegacyAccountingSourceCoverage(t *testing.T) {
	legacySourceRecipes(t, "service_state.go", map[string][]string{
		"ServiceGenerationActivationNeeded": {"storeRead"},
		"serviceCatalogPointer":             {"storeRead"},
		"GetAcceptedServiceStateSnapshot":   {"storeRead"},
		"getServiceStateEntryAtSnapshot":    {"storeRead"},
		"ListServiceStates":                 {"storeRead"},
		"commitServiceStateTransition":      {"storeUnsupported"},
		"getRawServiceStateSummary":         {"storeRead"},
		"serviceStatesForTransition":        {"storeRead", "storeRead"},
	}, false)
	legacySourceRecipes(t, "service_catalog.go", map[string][]string{
		"PublishServiceCatalog":               {"storeUnsupported"},
		"getVerifiedServiceCatalog":           {"storeRead"},
		"getVerifiedServiceCatalogGeneration": {"storeRead", "storeRead"},
	}, false)
}
