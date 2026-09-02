// Package recovery implements the version-bound live backup and fail-closed
// restore path. It operates only on the precious SurrealDB state; normal phebs
// startup remains responsible for rebuilding mirrors, indexes, and extraction.
package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ManifestSchema                       = "phebs-backup-manifest-v8"
	FocusedIndexArchiveReportSchema      = "phebs-focused-archive-report-v1"
	ResolverCatalogArchiveReportSchema   = "phebs-resolver-catalog-archive-report-v1"
	CallerPublicationArchiveReportSchema = "phebs-caller-publication-archive-report-v1"
	ObservationArchiveReportSchema       = "phebs-observation-archive-report-v2"
	RelationshipArchiveReportSchema      = "phebs-relationship-archive-report-v1"
	ManifestName                         = "manifest.json"
	DatabaseName                         = "database.surql"
	FocusedIndexName                     = "focused-index.tar"
	ResolverCatalogName                  = "resolver-catalog.tar"
	CallerPublicationName                = "caller-publication.tar"
	ObservationPublicationName           = "observation-publication.tar"
	RelationshipPublicationName          = "relationship-publication.tar"

	maxManifestBytes = 1 << 20
	maxCommandOutput = 64 << 10
	maxArtifactBytes = int64(1 << 40)
)

var derivedExclusions = []string{
	"index/ whole-repository zoekt shards (focused publications are preserved byte-exactly)",
	"repos/ (bare repository mirrors)",
	"candidates/ (content-addressed candidate manifests and partition rows)",
	"observation-plans/ (restartable source-partition planning state)",
	"relationship-schedules/ (restartable relationship scheduler bindings)",
	"relationship-v3-schedules/ (restartable v3 relationship scheduler bindings)",
	"invalid, in-flight, unreferenced, and non-current relationship component publications",
	"caller-leaves/ invalid, incomplete, marker-covered, and unreferenced derived caller publications",
	"temporary extraction and build caches",
}

var exportCommand = []string{
	"surreal", "export", "--endpoint", "<live-loopback-endpoint>",
	"--namespace", "phebs", "--database", "phebs", "--log", "none", DatabaseName,
}

type ToolIdentity struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type DatabaseIdentity struct {
	Namespace string `json:"namespace"`
	Database  string `json:"database"`
}

type Artifact struct {
	Path           string `json:"path"`
	Classification string `json:"classification"`
	MediaType      string `json:"media_type"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
}

// FocusedIndexArchiveReport is the durable omission receipt for derived
// focused state observed while the backup lock was held. Publications is
// independently checked against the exact archive during Verify.
type FocusedIndexArchiveReport struct {
	Schema              string `json:"schema"`
	Publications        int    `json:"publications"`
	OmittedPublications int    `json:"omitted_publications"`
	OmittedArtifacts    int    `json:"omitted_artifacts"`
	StaleMarkers        int    `json:"stale_markers"`
}

type ResolverCatalogArchiveReport struct {
	Schema              string                     `json:"schema"`
	Publications        int                        `json:"publications"`
	OmittedPublications int                        `json:"omitted_publications"`
	OmittedArtifacts    int                        `json:"omitted_artifacts"`
	StaleMarkers        int                        `json:"stale_markers"`
	Details             []resolvercatalog.Omission `json:"details,omitempty"`
	TruncatedDetails    int                        `json:"truncated_details"`
}

type CallerPublicationArchiveReport struct {
	Schema              string                       `json:"schema"`
	Publications        int                          `json:"publications"`
	OmittedPublications int                          `json:"omitted_publications"`
	OmittedArtifacts    int                          `json:"omitted_artifacts"`
	StaleMarkers        int                          `json:"stale_markers"`
	Details             []callerpublication.Omission `json:"details,omitempty"`
	TruncatedDetails    int                          `json:"truncated_details"`
}

type ObservationArchiveReport struct {
	Schema              string `json:"schema"`
	Publications        int    `json:"publications"`
	V1Publications      int    `json:"v1_publications"`
	V2Publications      int    `json:"v2_publications"`
	Files               int    `json:"files"`
	Bytes               int64  `json:"bytes"`
	Omitted             int    `json:"omitted"`
	OmittedPublications int    `json:"omitted_publications"`
	OmittedArtifacts    int    `json:"omitted_artifacts"`
	StaleMarkers        int    `json:"stale_markers"`
}

type RelationshipArchiveReport struct {
	Schema       string `json:"schema"`
	Publications int    `json:"publications"`
	Files        int    `json:"files"`
	Bytes        int64  `json:"bytes"`
	Omitted      int    `json:"omitted"`
}

// Manifest binds one database export to the exact recovery-compatible inputs.
// ManifestSHA256 digests the canonical JSON with that field empty, avoiding a
// recursive file hash while still detecting every meaningful manifest change.
type Manifest struct {
	Schema            string                         `json:"schema"`
	CreatedAt         time.Time                      `json:"created_at"`
	Database          DatabaseIdentity               `json:"database"`
	ConfigSHA256      string                         `json:"config_sha256"`
	Phebs             ToolIdentity                   `json:"phebs"`
	Surreal           ToolIdentity                   `json:"surreal"`
	Store             store.StoreIdentity            `json:"store"`
	ExportCommand     []string                       `json:"export_command"`
	Inventory         []Artifact                     `json:"inventory"`
	FocusedIndex      FocusedIndexArchiveReport      `json:"focused_index_archive"`
	ResolverCatalog   ResolverCatalogArchiveReport   `json:"resolver_catalog_archive"`
	CallerPublication CallerPublicationArchiveReport `json:"caller_publication_archive"`
	Observation       ObservationArchiveReport       `json:"observation_archive"`
	Relationship      RelationshipArchiveReport      `json:"relationship_archive"`
	DerivedExclusions []string                       `json:"derived_exclusions"`
	ManifestSHA256    string                         `json:"manifest_sha256"`
}

type Options struct {
	DataDir      string
	Config       []byte
	PhebsBinary  string
	PhebsVersion string
}

type BackupOptions struct {
	Options
	Output string
	Now    func() time.Time
}

type RestoreOptions struct {
	Options
	Backup string
}

// ConfigDigest is the canonical raw-config identity stored in runtime
// descriptors and backup manifests. Whitespace is intentionally significant.
func ConfigDigest(data []byte) string { return digestBytes(data) }

// Create exports a running local instance into an atomically published,
// private backup directory. Output must not already exist.
func Create(ctx context.Context, opts BackupOptions) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	output, err := absoluteCleanPath("backup output", opts.Output)
	if err != nil {
		return Manifest{}, err
	}
	if err := requireAbsent(output, "backup output"); err != nil {
		return Manifest{}, err
	}
	dataDir, err := absoluteCleanPath("data directory", opts.DataDir)
	if err != nil {
		return Manifest{}, err
	}
	runtime, err := store.ReadLocalRuntime(dataDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("discover live phebs store: %w", err)
	}
	configSHA256 := ConfigDigest(opts.Config)
	if runtime.ConfigSHA256 == "" || runtime.ConfigSHA256 != configSHA256 {
		return Manifest{}, errors.New("backup config digest differs from the live server configuration")
	}
	actualSurreal, err := store.InspectSurrealBinary(runtime.Surreal.Path)
	if err != nil {
		return Manifest{}, err
	}
	if actualSurreal != runtime.Surreal {
		return Manifest{}, errors.New("live SurrealDB identity changed after server startup")
	}
	phebs, err := inspectPhebs(ctx, opts.PhebsBinary, opts.PhebsVersion)
	if err != nil {
		return Manifest{}, err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create backup parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".phebs-backup-")
	if err != nil {
		return Manifest{}, fmt.Errorf("create backup staging directory: %w", err)
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.RemoveAll(stage)
		return Manifest{}, fmt.Errorf("protect backup staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	releaseBackup, err := focusedindex.AcquireBackupLock(
		ctx, filepath.Join(dataDir, "index"),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("acquire consistent index backup lock: %w", err)
	}
	defer releaseBackup()
	validationStore, err := store.Open(
		ctx, runtime.Endpoint, "root", "root", "phebs", "phebs",
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("open live store for catalog v3 validation: %w", err)
	}
	if _, err := validationStore.ValidateServiceCatalogV3Precious(ctx); err != nil {
		_ = validationStore.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("validate live catalog v3 precious inventory: %w", err)
	}
	selectors, err := validationStore.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		_ = validationStore.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("list live service runtime selectors: %w", err)
	}
	if err := validateServiceRuntimeSelections(
		ctx, dataDir, validationStore, selectors,
	); err != nil {
		_ = validationStore.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("validate live service runtime selections: %w", err)
	}
	if err := validationStore.Close(context.WithoutCancel(ctx)); err != nil {
		return Manifest{}, fmt.Errorf("close catalog v3 validation store: %w", err)
	}

	artifactPath := filepath.Join(stage, DatabaseName)
	args := []string{
		"export", "--endpoint", cliEndpoint(runtime.Endpoint),
		"--namespace", "phebs", "--database", "phebs", "--log", "none",
		artifactPath,
	}
	if err := runSurreal(ctx, actualSurreal.Path, args); err != nil {
		return Manifest{}, fmt.Errorf("export SurrealDB: %w", err)
	}
	if err := os.Chmod(artifactPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect database artifact: %w", err)
	}
	if err := syncFile(artifactPath); err != nil {
		return Manifest{}, err
	}
	artifact, err := inspectArtifact(
		ctx,
		artifactPath, DatabaseName, "precious", "application/surrealql",
	)
	if err != nil {
		return Manifest{}, err
	}
	searchSelections := make(
		[]focusedindex.ArchiveSearchGeneration, 0, len(selectors),
	)
	relationshipSelections := make(
		[]relationshippublication.ArchiveRelationshipGeneration, 0, len(selectors),
	)
	for _, selector := range selectors {
		searchSelections = append(searchSelections, focusedindex.ArchiveSearchGeneration{
			Repository: selector.Repository, GenerationDigest: selector.SearchGenerationDigest,
		})
		relationshipSelections = append(
			relationshipSelections,
			relationshippublication.ArchiveRelationshipGeneration{
				Repository:       selector.Repository,
				GenerationDigest: selector.RelationshipGenerationDigest,
				RootDigest:       selector.RelationshipRootDigest,
				V3:               selector.Backend == store.ServiceRuntimeV3,
			},
		)
	}
	focusedPath := filepath.Join(stage, FocusedIndexName)
	focusedReport, err := focusedindex.CreateArchiveWithSelections(
		ctx, filepath.Join(dataDir, "index"), focusedPath, searchSelections,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("archive focused index publications: %w", err)
	}
	if focusedReport.OmittedPublications > 0 ||
		focusedReport.OmittedArtifacts > 0 ||
		focusedReport.StaleMarkers > 0 {
		log.Printf(
			"backup focused derived state: archived=%d omitted_publications=%d omitted_artifacts=%d stale_markers=%d",
			focusedReport.Publications,
			focusedReport.OmittedPublications,
			focusedReport.OmittedArtifacts,
			focusedReport.StaleMarkers,
		)
	}
	if err := os.Chmod(focusedPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect focused-index artifact: %w", err)
	}
	if err := syncFile(focusedPath); err != nil {
		return Manifest{}, err
	}
	focusedArtifact, err := inspectArtifact(
		ctx,
		focusedPath, FocusedIndexName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	resolverPath := filepath.Join(stage, ResolverCatalogName)
	resolverReport, err := resolvercatalog.CreateArchiveWithReport(
		filepath.Join(dataDir, "resolver-catalogs"), resolverPath,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("archive resolver catalog publications: %w", err)
	}
	if resolverReport.OmittedPublications > 0 ||
		resolverReport.OmittedArtifacts > 0 ||
		resolverReport.StaleMarkers > 0 {
		log.Printf(
			"backup resolver catalog derived state: archived=%d omitted_publications=%d omitted_artifacts=%d stale_markers=%d truncated_details=%d",
			resolverReport.Publications,
			resolverReport.OmittedPublications,
			resolverReport.OmittedArtifacts,
			resolverReport.StaleMarkers,
			resolverReport.TruncatedDetails,
		)
	}
	if err := os.Chmod(resolverPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect resolver-catalog artifact: %w", err)
	}
	if err := syncFile(resolverPath); err != nil {
		return Manifest{}, err
	}
	resolverArtifact, err := inspectArtifact(
		ctx,
		resolverPath, ResolverCatalogName,
		"derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	callerPath := filepath.Join(stage, CallerPublicationName)
	callerReport, err := callerpublication.CreateArchiveWithReportContext(
		ctx, filepath.Join(dataDir, "caller-leaves"), callerPath,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("archive caller publications: %w", err)
	}
	if callerReport.OmittedPublications > 0 ||
		callerReport.OmittedArtifacts > 0 ||
		callerReport.StaleMarkers > 0 {
		log.Printf(
			"backup caller derived state: archived=%d omitted_publications=%d omitted_artifacts=%d stale_markers=%d truncated_details=%d",
			callerReport.Publications,
			callerReport.OmittedPublications,
			callerReport.OmittedArtifacts,
			callerReport.StaleMarkers,
			callerReport.TruncatedDetails,
		)
	}
	if err := os.Chmod(callerPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect caller-publication artifact: %w", err)
	}
	if err := syncFile(callerPath); err != nil {
		return Manifest{}, err
	}
	callerArtifact, err := inspectArtifact(
		ctx,
		callerPath, CallerPublicationName,
		"derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	observationPath := filepath.Join(stage, ObservationPublicationName)
	observationReport, err := observationpublication.CreateArchive(
		ctx, filepath.Join(dataDir, "observations"), observationPath,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("archive observation publications: %w", err)
	}
	if observationReport.Omitted > 0 {
		log.Printf(
			"backup observation derived state: archived=%d v1=%d v2=%d omitted_publications=%d omitted_artifacts=%d stale_markers=%d",
			observationReport.Publications, observationReport.V1Publications,
			observationReport.V2Publications, observationReport.OmittedPublications,
			observationReport.OmittedArtifacts, observationReport.StaleMarkers,
		)
	}
	if err := os.Chmod(observationPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect observation-publication artifact: %w", err)
	}
	if err := syncFile(observationPath); err != nil {
		return Manifest{}, err
	}
	observationArtifact, err := inspectArtifact(
		ctx, observationPath, ObservationPublicationName,
		"derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	relationshipPath := filepath.Join(stage, RelationshipPublicationName)
	relationshipReport, err := relationshippublication.CreateArchiveWithSelections(
		ctx, dataDir, relationshipPath, relationshipSelections,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("archive relationship publications: %w", err)
	}
	if relationshipReport.Omitted > 0 {
		log.Printf(
			"backup relationship derived state: archived=%d omitted=%d",
			relationshipReport.Publications, relationshipReport.Omitted,
		)
	}
	if err := os.Chmod(relationshipPath, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("protect relationship-publication artifact: %w", err)
	}
	if err := syncFile(relationshipPath); err != nil {
		return Manifest{}, err
	}
	relationshipArtifact, err := inspectArtifact(
		ctx, relationshipPath, RelationshipPublicationName,
		"derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	manifest := Manifest{
		Schema:       ManifestSchema,
		CreatedAt:    now().UTC(),
		Database:     DatabaseIdentity{Namespace: "phebs", Database: "phebs"},
		ConfigSHA256: configSHA256,
		Phebs:        phebs,
		Surreal: ToolIdentity{
			Version: actualSurreal.Version,
			SHA256:  actualSurreal.SHA256,
		},
		Store:         store.CurrentStoreIdentity(),
		ExportCommand: slices.Clone(exportCommand),
		Inventory: []Artifact{
			artifact, focusedArtifact, resolverArtifact, callerArtifact,
			observationArtifact, relationshipArtifact,
		},
		FocusedIndex: FocusedIndexArchiveReport{
			Schema:              FocusedIndexArchiveReportSchema,
			Publications:        focusedReport.Publications,
			OmittedPublications: focusedReport.OmittedPublications,
			OmittedArtifacts:    focusedReport.OmittedArtifacts,
			StaleMarkers:        focusedReport.StaleMarkers,
		},
		ResolverCatalog: ResolverCatalogArchiveReport{
			Schema:              ResolverCatalogArchiveReportSchema,
			Publications:        resolverReport.Publications,
			OmittedPublications: resolverReport.OmittedPublications,
			OmittedArtifacts:    resolverReport.OmittedArtifacts,
			StaleMarkers:        resolverReport.StaleMarkers,
			Details:             slices.Clone(resolverReport.Details),
			TruncatedDetails:    resolverReport.TruncatedDetails,
		},
		CallerPublication: CallerPublicationArchiveReport{
			Schema:              CallerPublicationArchiveReportSchema,
			Publications:        callerReport.Publications,
			OmittedPublications: callerReport.OmittedPublications,
			OmittedArtifacts:    callerReport.OmittedArtifacts,
			StaleMarkers:        callerReport.StaleMarkers,
			Details:             slices.Clone(callerReport.Details),
			TruncatedDetails:    callerReport.TruncatedDetails,
		},
		Observation: ObservationArchiveReport{
			Schema:         ObservationArchiveReportSchema,
			Publications:   observationReport.Publications,
			V1Publications: observationReport.V1Publications,
			V2Publications: observationReport.V2Publications,
			Files:          observationReport.Files, Bytes: observationReport.Bytes,
			Omitted:             observationReport.Omitted,
			OmittedPublications: observationReport.OmittedPublications,
			OmittedArtifacts:    observationReport.OmittedArtifacts,
			StaleMarkers:        observationReport.StaleMarkers,
		},
		Relationship: RelationshipArchiveReport{
			Schema:       RelationshipArchiveReportSchema,
			Publications: relationshipReport.Publications,
			Files:        relationshipReport.Files, Bytes: relationshipReport.Bytes,
			Omitted: relationshipReport.Omitted,
		},
		DerivedExclusions: slices.Clone(derivedExclusions),
	}
	manifest.ManifestSHA256, err = manifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeManifest(filepath.Join(stage, ManifestName), manifest); err != nil {
		return Manifest{}, err
	}
	if err := syncDirectory(stage); err != nil {
		return Manifest{}, err
	}
	// Recheck for a useful error, then use the platform's exclusive atomic
	// directory rename so a target created in this final window still wins.
	if err := requireAbsent(output, "backup output"); err != nil {
		return Manifest{}, err
	}
	if err := publishDirectory(stage, output); err != nil {
		return Manifest{}, fmt.Errorf("publish backup: %w", err)
	}
	published = true
	if err := syncDirectory(parent); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Restore verifies the complete backup and all compatibility identities before
// starting an exclusive import into an absent or empty data directory.
func Restore(ctx context.Context, opts RestoreOptions) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	target, err := absoluteCleanPath("data directory", opts.DataDir)
	if err != nil {
		return Manifest{}, err
	}
	if err := requireEmptyOrAbsent(target); err != nil {
		return Manifest{}, err
	}
	backup, err := absoluteCleanPath("backup", opts.Backup)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := VerifyContext(ctx, backup, opts.Options)
	if err != nil {
		return Manifest{}, err
	}
	// Close the validation/use race before creating the target. Once import
	// begins, any failure intentionally leaves a partial target that subsequent
	// restores refuse until the operator explicitly quarantines or removes it.
	if err := requireEmptyOrAbsent(target); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create restore data directory: %w", err)
	}
	// Restore observations first through a sibling stage. Until its final
	// rename, target remains empty, so an interrupted extraction is reachable
	// and resumable through this same top-level workflow.
	if err := observationpublication.RestoreArchiveWithStage(
		ctx, filepath.Join(backup, ObservationPublicationName),
		filepath.Join(target, "observations"), target+".observation-restore",
	); err != nil {
		return Manifest{}, fmt.Errorf("restore observation publications: %w", err)
	}
	runtime, stop, err := store.StartLocalImport(ctx, target)
	if err != nil {
		return Manifest{}, fmt.Errorf("start restore database: %w", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	if runtime.Surreal.Version != manifest.Surreal.Version || runtime.Surreal.SHA256 != manifest.Surreal.SHA256 {
		return Manifest{}, errors.New("restore SurrealDB identity differs from verified manifest")
	}
	args := []string{
		"import", "--endpoint", cliEndpoint(runtime.Endpoint),
		"--namespace", manifest.Database.Namespace, "--database", manifest.Database.Database,
		"--log", "none", filepath.Join(backup, DatabaseName),
	}
	if err := runSurreal(ctx, runtime.Surreal.Path, args); err != nil {
		return Manifest{}, fmt.Errorf("import SurrealDB: %w", err)
	}
	stop()
	stopped = true

	if err := focusedindex.RestoreArchive(
		filepath.Join(backup, FocusedIndexName), filepath.Join(target, "index"),
	); err != nil {
		return Manifest{}, fmt.Errorf("restore focused index publications: %w", err)
	}
	if err := resolvercatalog.RestoreArchive(
		filepath.Join(backup, ResolverCatalogName),
		filepath.Join(target, "resolver-catalogs"),
	); err != nil {
		return Manifest{}, fmt.Errorf("restore resolver catalog publications: %w", err)
	}
	if err := callerpublication.RestoreArchiveContext(
		ctx,
		filepath.Join(backup, CallerPublicationName),
		filepath.Join(target, "caller-leaves"),
	); err != nil {
		return Manifest{}, fmt.Errorf("restore caller publications: %w", err)
	}
	if err := relationshippublication.RestoreArchive(
		ctx, filepath.Join(backup, RelationshipPublicationName), target,
	); err != nil {
		return Manifest{}, fmt.Errorf("restore relationship publications: %w", err)
	}

	// Opening once applies the supported idempotent schema/migration set and
	// proves the imported database reaches the same application boundary.
	st, err := store.OpenLocal(ctx, target)
	if err != nil {
		return Manifest{}, fmt.Errorf("validate restored store: %w", err)
	}
	if _, err := st.RepairServiceCatalogV3Startup(ctx); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("repair restored catalog v3 state: %w", err)
	}
	relationshipReport, err := relationshippublication.RecoverAll(ctx, target, st)
	if err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("repair restored relationship publications: %w", err)
	}
	if relationshipReport.Invalid != 0 {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf(
			"repair restored relationship publications: %d invalid namespace(s)",
			relationshipReport.Invalid,
		)
	}
	if _, err := st.ValidateServiceCatalogV3Precious(ctx); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("validate restored catalog v3 inventory: %w", err)
	}
	if err := clearGenerationScheduleState(ctx, st); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf(
			"clear restartable generation schedules after restore: %w", err,
		)
	}
	// Clear imported caller authority first. Candidate and resolver bulk clears
	// also invalidate caller authority; this dedicated raw transition must own
	// the sole restore-time revision advance while the pointer still exists.
	if err := clearCallerPublicationState(ctx, st); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf(
			"clear derived caller publication state after restore: %w", err,
		)
	}
	if err := clearCandidateManifestPublications(ctx, st); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf(
			"clear derived candidate publications after restore: %w", err,
		)
	}
	if err := clearResolverCatalogPublications(ctx, st); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf(
			"clear derived resolver catalog publications after restore: %w", err,
		)
	}
	if err := ValidateServiceRuntimeSelections(ctx, target, st); err != nil {
		_ = st.Close(context.WithoutCancel(ctx))
		return Manifest{}, fmt.Errorf("validate restored service runtime selections: %w", err)
	}
	if err := st.Close(context.WithoutCancel(ctx)); err != nil {
		return Manifest{}, fmt.Errorf("close validated restored store: %w", err)
	}
	return manifest, nil
}

func clearGenerationScheduleState(
	ctx context.Context,
	state interface {
		ClearAllGenerationScheduleStateForRestore(context.Context) error
	},
) error {
	return state.ClearAllGenerationScheduleStateForRestore(ctx)
}

func clearCandidateManifestPublications(
	ctx context.Context,
	publications interface {
		ClearAllCandidateManifestPublications(context.Context) error
	},
) error {
	return publications.ClearAllCandidateManifestPublications(ctx)
}

func clearResolverCatalogPublications(
	ctx context.Context,
	publications interface {
		ClearAllResolverCatalogPublications(context.Context) error
	},
) error {
	return publications.ClearAllResolverCatalogPublications(ctx)
}

func clearCallerPublicationState(
	ctx context.Context,
	state interface {
		ClearAllCallerPublicationStateForRestore(context.Context) error
	},
) error {
	return state.ClearAllCallerPublicationStateForRestore(ctx)
}

// Verify performs offline artifact and compatibility validation without
// writing to the restore target.
func Verify(backup string, opts Options) (Manifest, error) {
	return VerifyContext(context.Background(), backup, opts)
}

// VerifyContext is the cancellable offline verification boundary.
func VerifyContext(
	ctx context.Context,
	backup string,
	opts Options,
) (Manifest, error) {
	if ctx == nil {
		return Manifest{}, errors.New("backup verification context is required")
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return Manifest{}, fmt.Errorf("backup entry %q is not a regular file", entry.Name())
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{
		CallerPublicationName, DatabaseName, FocusedIndexName, ManifestName,
		ObservationPublicationName, RelationshipPublicationName, ResolverCatalogName,
	}) {
		return Manifest{}, fmt.Errorf("backup inventory is incomplete or contains undeclared files: %v", names)
	}
	manifest, err := readManifest(filepath.Join(backup, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	wantDigest, err := manifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.ManifestSHA256 != wantDigest {
		return Manifest{}, errors.New("backup manifest digest mismatch")
	}
	if manifest.ConfigSHA256 != ConfigDigest(opts.Config) {
		return Manifest{}, errors.New("backup config digest differs from the supplied config")
	}
	phebs, err := inspectPhebs(ctx, opts.PhebsBinary, opts.PhebsVersion)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Phebs != phebs {
		return Manifest{}, errors.New("backup requires a different phebs binary")
	}
	surreal, err := store.FindSurrealBinary()
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Surreal.Version != surreal.Version || manifest.Surreal.SHA256 != surreal.SHA256 {
		return Manifest{}, errors.New("backup requires a different SurrealDB binary")
	}
	if manifest.Store != store.CurrentStoreIdentity() {
		return Manifest{}, errors.New("backup store identity is incompatible with this phebs binary")
	}
	actual, err := inspectArtifact(
		ctx,
		filepath.Join(backup, DatabaseName),
		DatabaseName, "precious", "application/surrealql",
	)
	if err != nil {
		return Manifest{}, err
	}
	if actual != manifest.Inventory[0] {
		return Manifest{}, errors.New("backup database artifact differs from its manifest")
	}
	focused, err := inspectArtifact(
		ctx,
		filepath.Join(backup, FocusedIndexName),
		FocusedIndexName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	if focused != manifest.Inventory[1] {
		return Manifest{}, errors.New("backup focused-index artifact differs from its manifest")
	}
	resolver, err := inspectArtifact(
		ctx,
		filepath.Join(backup, ResolverCatalogName),
		ResolverCatalogName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	if resolver != manifest.Inventory[2] {
		return Manifest{}, errors.New("backup resolver-catalog artifact differs from its manifest")
	}
	caller, err := inspectArtifact(
		ctx,
		filepath.Join(backup, CallerPublicationName),
		CallerPublicationName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	if caller != manifest.Inventory[3] {
		return Manifest{}, errors.New("backup caller-publication artifact differs from its manifest")
	}
	observation, err := inspectArtifact(
		ctx, filepath.Join(backup, ObservationPublicationName),
		ObservationPublicationName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	if observation != manifest.Inventory[4] {
		return Manifest{}, errors.New("backup observation-publication artifact differs from its manifest")
	}
	relationship, err := inspectArtifact(
		ctx, filepath.Join(backup, RelationshipPublicationName),
		RelationshipPublicationName, "derived-byte-exact", "application/x-tar",
	)
	if err != nil {
		return Manifest{}, err
	}
	if relationship != manifest.Inventory[5] {
		return Manifest{}, errors.New("backup relationship-publication artifact differs from its manifest")
	}
	focusedReport, err := focusedindex.VerifyArchiveWithReport(
		filepath.Join(backup, FocusedIndexName),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("verify focused-index artifact: %w", err)
	}
	if focusedReport.Publications != manifest.FocusedIndex.Publications {
		return Manifest{}, errors.New(
			"backup focused-index publication count differs from its manifest",
		)
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	resolverArchiveReport, err := resolvercatalog.VerifyArchiveWithReport(
		filepath.Join(backup, ResolverCatalogName),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("verify resolver-catalog artifact: %w", err)
	}
	if resolverArchiveReport.Publications != manifest.ResolverCatalog.Publications {
		return Manifest{}, errors.New(
			"backup resolver-catalog publication count differs from its manifest",
		)
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	callerArchiveReport, err := callerpublication.VerifyArchiveWithReportContext(
		ctx,
		filepath.Join(backup, CallerPublicationName),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("verify caller-publication artifact: %w", err)
	}
	if callerArchiveReport.Publications != manifest.CallerPublication.Publications {
		return Manifest{}, errors.New(
			"backup caller-publication count differs from its manifest",
		)
	}
	observationArchiveReport, err := observationpublication.VerifyArchive(
		ctx, filepath.Join(backup, ObservationPublicationName),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("verify observation-publication artifact: %w", err)
	}
	if observationArchiveReport.Publications != manifest.Observation.Publications ||
		observationArchiveReport.V1Publications != manifest.Observation.V1Publications ||
		observationArchiveReport.V2Publications != manifest.Observation.V2Publications ||
		observationArchiveReport.Files != manifest.Observation.Files ||
		observationArchiveReport.Bytes != manifest.Observation.Bytes {
		return Manifest{}, errors.New(
			"backup observation-publication report differs from its manifest",
		)
	}
	relationshipArchiveReport, err := relationshippublication.VerifyArchive(
		ctx, filepath.Join(backup, RelationshipPublicationName),
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("verify relationship-publication artifact: %w", err)
	}
	if relationshipArchiveReport.Publications != manifest.Relationship.Publications ||
		relationshipArchiveReport.Files != manifest.Relationship.Files ||
		relationshipArchiveReport.Bytes != manifest.Relationship.Bytes {
		return Manifest{}, errors.New(
			"backup relationship-publication report differs from its manifest",
		)
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema || manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC ||
		manifest.Database != (DatabaseIdentity{Namespace: "phebs", Database: "phebs"}) ||
		manifest.ConfigSHA256 == "" || manifest.Phebs.Version == "" || manifest.Phebs.SHA256 == "" ||
		manifest.Surreal.Version == "" || manifest.Surreal.SHA256 == "" || manifest.ManifestSHA256 == "" {
		return errors.New("backup manifest identity is incomplete")
	}
	if len(manifest.Inventory) != 6 || manifest.Inventory[0].Path != DatabaseName ||
		manifest.Inventory[0].Classification != "precious" ||
		manifest.Inventory[0].MediaType != "application/surrealql" || manifest.Inventory[0].Size <= 0 {
		return errors.New("backup manifest inventory is invalid")
	}
	if manifest.Inventory[1].Path != FocusedIndexName ||
		manifest.Inventory[1].Classification != "derived-byte-exact" ||
		manifest.Inventory[1].MediaType != "application/x-tar" ||
		manifest.Inventory[1].Size <= 0 {
		return errors.New("backup focused-index inventory is invalid")
	}
	if manifest.Inventory[2].Path != ResolverCatalogName ||
		manifest.Inventory[2].Classification != "derived-byte-exact" ||
		manifest.Inventory[2].MediaType != "application/x-tar" ||
		manifest.Inventory[2].Size <= 0 {
		return errors.New("backup resolver-catalog inventory is invalid")
	}
	if manifest.Inventory[3].Path != CallerPublicationName ||
		manifest.Inventory[3].Classification != "derived-byte-exact" ||
		manifest.Inventory[3].MediaType != "application/x-tar" ||
		manifest.Inventory[3].Size <= 0 {
		return errors.New("backup caller-publication inventory is invalid")
	}
	if manifest.Inventory[4].Path != ObservationPublicationName ||
		manifest.Inventory[4].Classification != "derived-byte-exact" ||
		manifest.Inventory[4].MediaType != "application/x-tar" ||
		manifest.Inventory[4].Size <= 0 {
		return errors.New("backup observation-publication inventory is invalid")
	}
	if manifest.Inventory[5].Path != RelationshipPublicationName ||
		manifest.Inventory[5].Classification != "derived-byte-exact" ||
		manifest.Inventory[5].MediaType != "application/x-tar" ||
		manifest.Inventory[5].Size <= 0 {
		return errors.New("backup relationship-publication inventory is invalid")
	}
	if manifest.FocusedIndex.Schema != FocusedIndexArchiveReportSchema ||
		manifest.FocusedIndex.Publications < 0 ||
		manifest.FocusedIndex.OmittedPublications < 0 ||
		manifest.FocusedIndex.OmittedArtifacts < 0 ||
		manifest.FocusedIndex.StaleMarkers < 0 {
		return errors.New("backup focused-index archive report is invalid")
	}
	if manifest.ResolverCatalog.Schema != ResolverCatalogArchiveReportSchema ||
		manifest.ResolverCatalog.Publications < 0 ||
		manifest.ResolverCatalog.OmittedPublications < 0 ||
		manifest.ResolverCatalog.OmittedArtifacts < 0 ||
		manifest.ResolverCatalog.StaleMarkers < 0 ||
		len(manifest.ResolverCatalog.Details) > resolvercatalog.MaxOmissionDetails ||
		manifest.ResolverCatalog.TruncatedDetails < 0 {
		return errors.New("backup resolver-catalog archive report is invalid")
	}
	for _, detail := range manifest.ResolverCatalog.Details {
		if detail.Name == "" || len(detail.Name) > 512 ||
			(detail.Reason != "invalid_manifest" &&
				detail.Reason != "publication_marker" &&
				detail.Reason != "invalid_publication" &&
				detail.Reason != "unreferenced_artifact") {
			return errors.New("backup resolver-catalog omission detail is invalid")
		}
	}
	if manifest.CallerPublication.Schema != CallerPublicationArchiveReportSchema ||
		manifest.CallerPublication.Publications < 0 ||
		manifest.CallerPublication.OmittedPublications < 0 ||
		manifest.CallerPublication.OmittedArtifacts < 0 ||
		manifest.CallerPublication.StaleMarkers < 0 ||
		len(manifest.CallerPublication.Details) > callerpublication.MaxOmissionDetails ||
		manifest.CallerPublication.TruncatedDetails < 0 {
		return errors.New("backup caller-publication archive report is invalid")
	}
	for _, detail := range manifest.CallerPublication.Details {
		if detail.Name == "" || len(detail.Name) > 512 ||
			(detail.Reason != "invalid_manifest" &&
				detail.Reason != "ambiguous_publication" &&
				detail.Reason != "publication_marker" &&
				detail.Reason != "invalid_publication" &&
				detail.Reason != "unreferenced_artifact") {
			return errors.New("backup caller-publication omission detail is invalid")
		}
	}
	if manifest.Observation.Schema != ObservationArchiveReportSchema ||
		manifest.Observation.Publications < 0 ||
		manifest.Observation.Publications != manifest.Observation.V1Publications+manifest.Observation.V2Publications ||
		manifest.Observation.V1Publications < 0 || manifest.Observation.V2Publications < 0 ||
		manifest.Observation.Files < 0 || manifest.Observation.Bytes < 0 ||
		manifest.Observation.Omitted < 0 ||
		manifest.Observation.Omitted != manifest.Observation.OmittedPublications+manifest.Observation.OmittedArtifacts ||
		manifest.Observation.OmittedPublications < 0 || manifest.Observation.OmittedArtifacts < 0 ||
		manifest.Observation.StaleMarkers < 0 ||
		manifest.Observation.StaleMarkers > manifest.Observation.OmittedArtifacts {
		return errors.New("backup observation archive report is invalid")
	}
	if manifest.Relationship.Schema != RelationshipArchiveReportSchema ||
		manifest.Relationship.Publications < 0 || manifest.Relationship.Files < 0 ||
		manifest.Relationship.Bytes < 0 || manifest.Relationship.Omitted < 0 {
		return errors.New("backup relationship archive report is invalid")
	}
	if !slices.Equal(manifest.DerivedExclusions, derivedExclusions) {
		return errors.New("backup manifest derived-state classification is invalid")
	}
	if !slices.Equal(manifest.ExportCommand, exportCommand) {
		return errors.New("backup manifest export command is invalid")
	}
	for _, digest := range []string{
		manifest.ConfigSHA256, manifest.Phebs.SHA256, manifest.Surreal.SHA256,
		manifest.Inventory[0].SHA256, manifest.Inventory[1].SHA256,
		manifest.Inventory[2].SHA256, manifest.Inventory[3].SHA256,
		manifest.Inventory[4].SHA256, manifest.Inventory[5].SHA256,
		manifest.ManifestSHA256,
	} {
		if !validSHA256(digest) {
			return errors.New("backup manifest contains an invalid SHA-256 digest")
		}
	}
	return nil
}

func inspectArtifact(
	ctx context.Context,
	path, name, classification, mediaType string,
) (Artifact, error) {
	if ctx == nil {
		return Artifact{}, errors.New("artifact inspection context is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("inspect %s artifact: %w", name, err)
	}
	limit := artifactByteLimit(name)
	if !info.Mode().IsRegular() || !validArtifactSize(name, info.Size()) {
		return Artifact{}, fmt.Errorf("%s artifact is empty, special, or exceeds its limit", name)
	}
	digest, err := digestFile(ctx, path, limit)
	if err != nil {
		return Artifact{}, fmt.Errorf("digest %s artifact: %w", name, err)
	}
	return Artifact{
		Path: name, Classification: classification, MediaType: mediaType,
		Size: info.Size(), SHA256: digest,
	}, nil
}

func artifactByteLimit(name string) int64 {
	if name == CallerPublicationName {
		return callerpublication.MaxArchiveBytes
	}
	return maxArtifactBytes
}

func validArtifactSize(name string, size int64) bool {
	return size > 0 && size <= artifactByteLimit(name)
}

func inspectPhebs(
	ctx context.Context,
	path, version string,
) (ToolIdentity, error) {
	if ctx == nil {
		return ToolIdentity{}, errors.New("phebs inspection context is required")
	}
	if strings.TrimSpace(version) != version || version == "" {
		return ToolIdentity{}, errors.New("phebs version is empty or padded")
	}
	if path == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			return ToolIdentity{}, fmt.Errorf("resolve phebs binary: %w", err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ToolIdentity{}, fmt.Errorf("resolve phebs binary path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ToolIdentity{}, fmt.Errorf("resolve phebs binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return ToolIdentity{}, fmt.Errorf("inspect phebs binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Size() > 1<<30 {
		return ToolIdentity{}, errors.New("phebs binary is not a bounded executable regular file")
	}
	digest, err := digestFile(ctx, resolved, 1<<30)
	if err != nil {
		return ToolIdentity{}, fmt.Errorf("digest phebs binary: %w", err)
	}
	return ToolIdentity{Version: version, SHA256: digest}, nil
}

func runSurreal(ctx context.Context, binary string, args []string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "SURREAL_USER=root", "SURREAL_PASS=root")
	output := &boundedOutput{limit: maxCommandOutput}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

// cliEndpoint translates the SDK's WebSocket endpoint into the HTTP endpoint
// used by SurrealDB's export/import CLI and its streaming HTTP routes.
func cliEndpoint(endpoint string) string {
	return "http://" + strings.TrimPrefix(endpoint, "ws://")
}

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	want := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:min(len(p), remaining)])
	}
	return want, nil
}

func (w *boundedOutput) String() string { return w.buffer.String() }

func readManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return Manifest{}, errors.New("backup manifest is missing, special, or exceeds its limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("decode backup manifest: trailing content")
	}
	return manifest, nil
}

func writeManifest(path string, manifest Manifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create backup manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync backup manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup manifest: %w", err)
	}
	return nil
}

func manifestDigest(manifest Manifest) (string, error) {
	manifest.ManifestSHA256 = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode manifest digest payload: %w", err)
	}
	return digestBytes(data), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digestFile(ctx context.Context, path string, maxBytes int64) (string, error) {
	if ctx == nil {
		return "", errors.New("file digest context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(
		hash,
		io.LimitReader(contextReader{ctx: ctx, reader: file}, maxBytes+1),
	)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", errors.New("file exceeds its digest limit")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(raw []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(raw)
}

func absoluteCleanPath(name, raw string) (string, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return "", fmt.Errorf("%s path is empty or padded", name)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return filepath.Clean(abs), nil
}

func requireAbsent(path, name string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", name)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	return nil
}

func requireEmptyOrAbsent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect restore data directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("restore data directory exists and is not a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read restore data directory: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("restore data directory is not empty; refusing a partial or existing target")
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file for sync: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}
