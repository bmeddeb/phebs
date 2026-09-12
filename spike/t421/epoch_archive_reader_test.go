package t421

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/recovery"
)

// Wire fixtures use the existing real HTTP/PC token helper, but are not actual
// archive files, process joins or authenticated constructor admission.
func archiveReaderManifest() recovery.ArchiveTransitionManifest {
	value := recovery.ArchiveTransitionManifest{ManifestSchema: recovery.ManifestSchema, ManifestSHA256: testDigest("manifest")}
	for _, name := range []string{recovery.DatabaseName, recovery.FocusedIndexName, recovery.ResolverCatalogName, recovery.CallerPublicationName, recovery.ObservationPublicationName, recovery.RelationshipPublicationName} {
		component := recovery.ArchiveTransitionComponent{Name: name, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest(name)}
		if name == recovery.DatabaseName {
			component.Classification, component.MediaType = "precious", "application/surrealql"
		}
		value.Components = append(value.Components, component)
	}
	for i, name := range []string{"focused_index", "resolver_catalog", "caller_publication", "observation", "relationship"} {
		schema := []string{recovery.FocusedIndexArchiveReportSchema, recovery.ResolverCatalogArchiveReportSchema, recovery.CallerPublicationArchiveReportSchema, recovery.ObservationArchiveReportSchema, recovery.RelationshipArchiveReportSchema}[i]
		row := recovery.ArchiveTransitionReport{Name: name, Schema: schema, Publications: 1}
		if i >= 3 {
			row.Files, row.Bytes = 1, 1
		}
		if i == 3 {
			row.V2Publications = 1
		}
		value.Reports = append(value.Reports, row)
	}
	return value
}

func configureArchiveTestReader(t *testing.T, reader *executionEpochInspection) {
	t.Helper()
	reader.run.epoch.Epoch = 5
	reader.archiveInput = epochArchiveInput{BackupRoot: "t422-backup-123", Inode: 1, FSID: [2]int32{1, 2},
		BackupCommandSHA256: testDigest("manifest"), RestoreCommandSHA256: testDigest("manifest")}
	rows, epochs, err := correctedInspectionInventory(reader.plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	reader.bounds, reader.projection.Phase = rows[11], "archive_restore"
	for _, epoch := range epochs {
		if epoch.ServerEpoch == 5 {
			reader.maximumReports = epoch.AccountedServerRequestsMaximum
		}
	}
	if reader.maximumReports != 8691 {
		t.Fatal("epoch-five inventory changed", reader.maximumReports)
	}
}

func TestEpochArchiveReaderConstructorRefusesUnowned(t *testing.T) {
	// No successful constructor is modeled: these cases must fail before any
	// metadata read without an actual held author/epoch-five custody owner.
	for _, mode := range []string{"nil_context", "wrong_epoch", "unhealthy", "input", "invalid_input", "prior", "health_pending", "stopping", "stopped", "duplicate", "borrow"} {
		t.Run(mode, func(t *testing.T) {
			wire := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("constructor issued HTTP") }))
			configureArchiveTestReader(t, wire)
			run := wire.run
			run.flow = &ExecutionEpochOne{plan: wire.plan, epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}}}
			run.healthy, run.healthDone = true, make(chan struct{})
			if mode != "health_pending" {
				close(run.healthDone)
			}
			run.archiveInput = &wire.archiveInput
			run.archivePrior = &AuthorityPhaseResult{Phase: "pressure_75", Outcome: "passed"}
			ctx := t.Context()
			switch mode {
			case "nil_context":
				ctx = nil
			case "wrong_epoch":
				run.epoch.Epoch = 4
			case "unhealthy":
				run.healthy = false
			case "input":
				run.archiveInput = nil
			case "invalid_input":
				run.archiveInput.BackupRoot = "../outside"
			case "prior":
				run.archivePrior = nil
			case "stopping":
				run.stopping = true
			case "stopped":
				close(run.stop)
			case "duplicate":
				run.inspection = wire
			}
			if result, err := run.newArchiveInspection(ctx); err == nil || result != nil {
				t.Fatal("unowned archive constructor admitted")
			}
			if mode == "borrow" && (run.inspection == nil || run.inspection.err == nil) {
				t.Fatal("refused issuer reservation was lost")
			}
		})
	}
}

func TestEpochArchiveReaderLastOrdinal(t *testing.T) {
	// Position only is modeled; the HTTP response and trailer are real. This
	// does not claim that the preceding 8,690 operations have been performed.
	calls := 0
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "8691" {
			t.Error("last ordinal changed")
		}
		w.Header().Set("Trailer", epochReadTrailer)
		_, _ = w.Write([]byte("{}\n"))
		report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 8691}
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
	}))
	configureArchiveTestReader(t, reader)
	reader.next = reader.maximumReports
	if _, _, _, err := reader.read(t.Context(), "/api/t421/final-authority", 3, epochInspectionReport{}); err != nil || reader.next != 8692 || calls != 1 {
		t.Fatal("last allowed ordinal refused", err, reader.next, calls)
	}
	if _, _, _, err := reader.read(t.Context(), "/api/t421/final-authority", 3, epochInspectionReport{}); err == nil || calls != 1 {
		t.Fatal("extra ordinal issued HTTP", err, calls)
	}
}

func TestEpochArchiveReaderWire(t *testing.T) {
	for _, mode := range []string{"valid", "missing_trailer", "zero_read", "extra_read", "store_read", "member_read", "write", "ordinal", "status", "digest", "components", "zero_component", "report_count", "zero_publication", "report_schema", "observation_sum", "relationship_bytes", "unknown_omission", "noncanonical", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/t422/archive/transition" || r.URL.RawQuery != "" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "1" {
					t.Error("archive route/ordinal changed")
				}
				value := archiveReaderManifest()
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: 1, Status: "complete", ControlFileReads: 1}
				switch mode {
				case "zero_read":
					report.ControlFileReads = 0
				case "extra_read":
					report.ControlFileReads = 2
				case "store_read":
					report.StoreReadAttempts = 1
				case "member_read":
					report.MemberVisits = 1
				case "write":
					report.StoreWriteAttempts = 1
				case "ordinal":
					report.RequestOrdinal = 2
				case "digest":
					value.ManifestSHA256 = testDigest("wrong")
				case "components":
					value.Components = value.Components[:5]
				case "zero_component":
					value.Components[0].Bytes = 0
				case "report_count":
					value.Reports = value.Reports[:4]
				case "zero_publication":
					value.Reports[0].Publications = 0
				case "report_schema":
					value.Reports[0].Schema = "wrong"
				case "observation_sum":
					value.Reports[3].V2Publications++
				case "relationship_bytes":
					value.Reports[4].Bytes = 0
				}
				raw := epochTestJSON(t, value, false)
				if mode == "unknown_omission" {
					raw = bytes.Replace(raw, []byte(`"publications":1`), []byte(`"publications":1,"omitted":1`), 1)
				}
				if mode == "noncanonical" {
					raw = append(raw, '\n')
				}
				if mode != "missing_trailer" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				if mode == "truncated" {
					w.Header().Set("Content-Length", strconv.Itoa(len(raw)+10))
				}
				if mode == "status" {
					w.WriteHeader(http.StatusConflict)
				}
				_, _ = w.Write(raw)
				if mode != "missing_trailer" {
					w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
				}
			}))
			configureArchiveTestReader(t, reader)
			result, _, err := reader.Archive(t.Context())
			if (err == nil) != (mode == "valid") || calls != 1 || reader.next != 2 {
				t.Fatal(mode, calls, reader.next, err)
			}
			if mode == "valid" {
				result.Components[0].Bytes++
				if reader.archiveManifest == nil || reader.archiveManifest.Components[0].Bytes != 1 || reader.totals.ControlFileReads != 1 {
					t.Fatal("validated R not privately retained")
				}
			}
			if _, _, err := reader.Archive(t.Context()); err == nil || calls != 1 {
				t.Fatal("R retry admitted", err, calls)
			}
		})
	}
}

func TestEpochArchiveReaderOrdinalAndOrder(t *testing.T) {
	for _, mode := range []string{"missing_R", "epoch_bound", "unbound_epoch"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("refusal issued HTTP") }))
			configureArchiveTestReader(t, reader)
			if mode == "missing_R" {
				if _, _, err := reader.Progress(t.Context()); err == nil {
					t.Fatal("X preceded R")
				}
			} else {
				reader.next = reader.maximumReports + 1
				if mode == "unbound_epoch" {
					reader.maximumReports, reader.next = 0, 1
				}
				if _, _, _, err := reader.read(t.Context(), "/api/t422/archive/transition", 1<<20, epochInspectionReport{ControlFileReads: 1}); err == nil {
					t.Fatal("epoch ordinal ceiling bypassed")
				}
			}
		})
	}
}

func TestEpochArchiveReaderTail(t *testing.T) {
	for _, mode := range []string{"same", "both_relationship", "generation_only", "root_only", "caller_changed"} {
		t.Run(mode, func(t *testing.T) {
			prior := AuthorityPhaseResult{AuthorityState: AuthorityState{RelationshipGenerationSHA256: testDigest("generation"), RelationshipRootSHA256: testDigest("root"), CallerGenerationSHA256: testDigest("caller"), CallerRootSHA256: testDigest("callerroot")}}
			value := epochTailReadiness{Schema: "t421-tail-readiness-source-free-v1", Status: "ready", SelectedRuntimeSHA256: testDigest("runtime"),
				RelationshipGenerationSHA256: prior.RelationshipGenerationSHA256, RelationshipRootSHA256: prior.RelationshipRootSHA256,
				CallerGenerationSHA256: prior.CallerGenerationSHA256, CallerRootSHA256: prior.CallerRootSHA256}
			switch mode {
			case "both_relationship":
				value.RelationshipGenerationSHA256, value.RelationshipRootSHA256 = testDigest("nextgen"), testDigest("nextroot")
			case "generation_only":
				value.RelationshipGenerationSHA256 = testDigest("nextgen")
			case "root_only":
				value.RelationshipRootSHA256 = testDigest("nextroot")
			case "caller_changed":
				value.CallerRootSHA256 = testDigest("nextcaller")
			}
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/t421/tail-readiness" {
					t.Error("wrong tail route")
				}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, false))
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 1, ControlFileReads: 4, StoreReadAttempts: 4}
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			configureArchiveTestReader(t, reader)
			reader.archivePrior, reader.progressReady = prior, true
			result, _, err := reader.Tail(t.Context())
			ready := mode == "same" || mode == "both_relationship"
			if err != nil || (result.Status == "ready") != ready || reader.totals.ControlFileReads != 4 || reader.totals.StoreReadAttempts != 4 {
				t.Fatal("archive tail continuity", result, err)
			}
		})
	}
}

func TestEpochArchiveReaderFinal(t *testing.T) {
	base, native := epochTestFinal(t)
	physical := base.plan.Revisions.Physical[2]
	projection, err := expectedStateProjectionForPhase(base.plan, "archive_restore")
	if err != nil {
		t.Fatal(err)
	}
	native.Authority.PhysicalCommit, native.Authority.PhysicalTree = physical.ExpectedCommit, physical.ExpectedTree
	state := AuthorityState{}
	raw, _ := json.Marshal(native.Authority)
	if json.Unmarshal(raw, &state) != nil {
		t.Fatal("fixture authority")
	}
	native.ExtractionRoots = testExtractionRoots(t, base.plan, physical, state, "archive-fixture")
	for i := range native.ExtractionRoots {
		native.ExtractionRoots[i].ScheduleSHA256 = ""
		native.ExtractionRoots[i].Members, err = extractionResultMembers(native.ExtractionRoots[i].PartitionResults)
		if err != nil {
			t.Fatal(err)
		}
	}
	native.Authority.ExtractionRootsSHA256 = mustReceiptSHA256(t, native.ExtractionRoots)
	raw, _ = json.Marshal(projection)
	if json.Unmarshal(raw, &native.Projection) != nil {
		t.Fatal("fixture projection")
	}
	native.Projection.Schema = "t421-final-state-projection-source-free-v1"
	raw, _ = json.Marshal(native.Authority)
	if json.Unmarshal(raw, &state) != nil {
		t.Fatal("fixture prior")
	}
	state.PhysicalRevision, state.LogicalRevision = "a-return", "a-return"
	prior := AuthorityPhaseResult{Phase: "pressure_75", Outcome: "passed", AuthorityState: state, ExtractionRoots: native.ExtractionRoots}
	for _, mode := range []string{"same", "rebuilt", "root_only", "provenance_only", "bad_provenance", "detailed_root", "projection", "caller", "missing_R"} {
		t.Run(mode, func(t *testing.T) {
			var value epochFinalResponse
			if decodeEpochJSON(epochTestJSON(t, native, true), &value, true) != nil {
				t.Fatal("fixture clone")
			}
			reader := &executionEpochInspection{run: &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 5}}, plan: base.plan, projection: projection, archivePrior: cloneArchiveAuthority(prior), archiveManifest: &recovery.ArchiveTransitionManifest{}}
			switch mode {
			case "rebuilt":
				value.Authority.RelationshipGenerationSHA256, value.Authority.RelationshipRootSHA256, value.Authority.RelationshipProvenanceSHA256 = testDigest("newgen"), testDigest("newroot"), testDigest("newprovenance")
			case "root_only":
				value.Authority.RelationshipRootSHA256 = testDigest("newroot")
			case "provenance_only":
				value.Authority.RelationshipProvenanceSHA256 = testDigest("newprovenance")
			case "bad_provenance":
				value.Authority.RelationshipGenerationSHA256, value.Authority.RelationshipRootSHA256, value.Authority.RelationshipProvenanceSHA256 = testDigest("newgen"), testDigest("newroot"), "invalid"
			case "detailed_root":
				value.ExtractionRoots[0].Totals.Rows++
			case "projection":
				value.Projection.Catalog.Records++
			case "caller":
				value.Authority.CallerGenerationSHA256 = testDigest("newcaller")
			case "missing_R":
				reader.archiveManifest = nil
			}
			reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: value.Authority.RelationshipGenerationSHA256, RelationshipRootSHA256: value.Authority.RelationshipRootSHA256, CallerGenerationSHA256: value.Authority.CallerGenerationSHA256, CallerRootSHA256: value.Authority.CallerRootSHA256}
			if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); (err == nil) != (mode == "same" || mode == "rebuilt") {
				t.Fatal(mode, err)
			}
		})
	}
	// Actual HTTP/trailer mechanics retain a detached validated F, not selector
	// acceptance. These response roots remain explicitly modeled fixture data.
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/t421/final-authority" {
			t.Error("wrong F route")
		}
		w.Header().Set("Trailer", epochReadTrailer)
		_, _ = w.Write(epochTestJSON(t, native, true))
		report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 1}
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
	}))
	configureArchiveTestReader(t, reader)
	reader.projection, reader.archivePrior, reader.progressReady = projection, cloneArchiveAuthority(prior), true
	reader.archiveManifest = &recovery.ArchiveTransitionManifest{}
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: native.Authority.RelationshipGenerationSHA256, RelationshipRootSHA256: native.Authority.RelationshipRootSHA256, CallerGenerationSHA256: native.Authority.CallerGenerationSHA256, CallerRootSHA256: native.Authority.CallerRootSHA256}
	accepted, _, _, err := reader.Final(t.Context())
	if err != nil || reader.archiveAuthority.Phase != "archive_restore" || reader.evidence.rows[0].SelectorAccepted {
		t.Fatal("F retention", err)
	}
	accepted.ExtractionRoots[0].PartitionResults[0].Totals.Rows++
	if reader.archiveAuthority.ExtractionRoots[0].PartitionResults[0].Totals == accepted.ExtractionRoots[0].PartitionResults[0].Totals {
		t.Fatal("returned F aliases retained authority")
	}
}
