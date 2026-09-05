// Package servicecatalogingest binds the pure T33.1 catalog to one exact
// repository source census and publishes its immutable T33.2 authority.
package servicecatalogingest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type repositoryStore interface {
	store.ServiceCatalogPublicationStore
	GetRepo(context.Context, string) (*store.Repo, error)
	ListRepos(context.Context) ([]store.Repo, error)
	ReconcileServiceStates(context.Context, servicecatalog.Publication) error
}

// Reconciler reads only explicitly selected inputs. It never guesses a
// service from directory shape and never clears a prior current authority.
type Reconciler struct {
	DataDir      string
	Store        repositoryStore
	Selections   map[string]config.ServiceCatalog
	WithMutation func(context.Context, string, servicecatalog.Publication, func() error) error
	OnPublished  func(context.Context, string) error
}

type Outcome string

const (
	OutcomeCurrent        Outcome = "current"
	OutcomePublished      Outcome = "published"
	OutcomeLegacyImported Outcome = "legacy_imported"
	OutcomeNotReady       Outcome = "not_ready"
	OutcomeUnselected     Outcome = "unselected"
)

type Failure struct {
	Repository string
	Err        error
}

type Report struct {
	Current        int
	Published      int
	LegacyImported int
	NotReady       int
	Unselected     int
	Failures       []Failure
}

// Reconcile processes each live repository independently. Invalid replacement
// input is reported without preventing unrelated repositories from publishing.
func (r *Reconciler) Reconcile(ctx context.Context) (Report, error) {
	if r.Store == nil {
		return Report{}, errors.New("service catalog reconciler requires a store")
	}
	repositories, err := r.Store.ListRepos(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list repositories: %w", err)
	}
	report := Report{Failures: []Failure{}}
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		outcome, reconcileErr := r.reconcile(ctx, repository)
		if reconcileErr == nil {
			reconcileErr = r.afterPublish(ctx, repository.Name, outcome)
		}
		if reconcileErr != nil {
			report.Failures = append(report.Failures, Failure{
				Repository: repository.Name, Err: reconcileErr,
			})
			continue
		}
		report.add(outcome)
	}
	return report, nil
}

// ReconcileRepository performs the same exact transition after an index
// publication, closing the startup-to-next-restart gap.
func (r *Reconciler) ReconcileRepository(
	ctx context.Context,
	repository string,
) (Outcome, error) {
	if r.Store == nil {
		return "", errors.New("service catalog reconciler requires a store")
	}
	current, err := r.Store.GetRepo(ctx, repository)
	if err != nil {
		return "", err
	}
	outcome, err := r.reconcile(ctx, *current)
	if err == nil {
		err = r.afterPublish(ctx, repository, outcome)
	}
	return outcome, err
}

func (r *Reconciler) afterPublish(
	ctx context.Context, repository string, outcome Outcome,
) error {
	if r.OnPublished == nil ||
		(outcome != OutcomeCurrent && outcome != OutcomePublished &&
			outcome != OutcomeLegacyImported) {
		return nil
	}
	return r.OnPublished(ctx, repository)
}

func (r *Reconciler) reconcile(
	ctx context.Context,
	repository store.Repo,
) (Outcome, error) {
	if repository.Deleting || repository.IndexedCommitHash == "" {
		return OutcomeNotReady, nil
	}
	selection, selected := r.Selections[repository.Name]
	if !selected && repository.IndexedAnalysisUnit == nil {
		// Absence is not guessed removal authority. T33.3 owns lifecycle and
		// tombstones, so an ordinary whole-repository row pays no point read.
		return OutcomeUnselected, nil
	}
	current, currentErr := r.Store.GetServiceCatalog(ctx, repository.Name)
	if currentErr != nil && !errors.Is(currentErr, store.ErrNotFound) {
		return "", fmt.Errorf("strict-open prior authority: %w", currentErr)
	}
	if selected {
		return r.reconcileV2(ctx, repository, selection, current)
	}
	return r.reconcileLegacy(ctx, repository, current)
}

func (r *Reconciler) reconcileV2(
	ctx context.Context,
	repository store.Repo,
	selection config.ServiceCatalog,
	current *servicecatalog.Publication,
) (Outcome, error) {
	head := repository.IndexedCommitHash
	content, err := readCatalog(selection)
	if err != nil {
		return "", err
	}
	catalog, err := servicecatalog.Decode(content)
	if err != nil {
		return "", fmt.Errorf("decode selected catalog: %w", err)
	}
	wantAuthority := servicecatalog.Authority{
		Kind: selection.Kind, ID: selection.ID, Version: selection.Version,
	}
	if selection.Kind == servicecatalog.AuthorityCommitted {
		wantAuthority.Version = head
	}
	if catalog.Authority != wantAuthority {
		return "", errors.New("selected catalog authority does not match explicit configuration")
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		return "", err
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		return "", err
	}
	if current != nil && current.SourceKind == selection.Kind &&
		current.SourcePath == selection.Path && current.SourceCommit == head &&
		current.CatalogDigest == catalogDigest && current.LegacyAnalysisUnitDigest == "" {
		// Operator inputs are read once on restart to catch version reuse, but
		// exact catalog/source identity avoids the repository census entirely.
		// T33.3 still strict-reconciles its point summary so a crash between the
		// catalog and state transactions repairs on retry.
		if err := r.withMutation(ctx, repository.Name, *current, func() error {
			return r.Store.ReconcileServiceStates(ctx, *current)
		}); err != nil {
			return "", err
		}
		return OutcomeCurrent, nil
	}
	census, err := r.census(ctx, repository.Name, head, catalog, false)
	if err != nil {
		return "", err
	}
	publication, err := newPublication(
		repository.Name, selection.Kind, selection.Path, head, "",
		catalog, canonical, catalogDigest, census,
	)
	if err != nil {
		return "", err
	}
	if err := r.withMutation(ctx, repository.Name, publication, func() error {
		if err := r.Store.PublishServiceCatalog(ctx, publication); err != nil {
			return err
		}
		return r.reconcilePublishedServiceStates(ctx, repository.Name)
	}); err != nil {
		return "", err
	}
	return OutcomePublished, nil
}

func (r *Reconciler) reconcileLegacy(
	ctx context.Context,
	repository store.Repo,
	current *servicecatalog.Publication,
) (Outcome, error) {
	unit := repository.IndexedAnalysisUnit
	if current != nil && current.SourceKind == servicecatalog.SourceAnalysisUnitV1 &&
		current.SourceCommit == repository.IndexedCommitHash &&
		current.LegacyAnalysisUnitDigest == unit.Digest {
		if err := r.withMutation(ctx, repository.Name, *current, func() error {
			return r.Store.ReconcileServiceStates(ctx, *current)
		}); err != nil {
			return "", err
		}
		return OutcomeCurrent, nil
	}
	catalog := legacyCatalog(unit)
	census, err := r.census(
		ctx, repository.Name, repository.IndexedCommitHash, catalog, true,
	)
	if err != nil {
		return "", err
	}
	catalog.Unowned = census.LegacyUnowned
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		return "", fmt.Errorf("canonicalize analysis-unit-v1 import: %w", err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		return "", err
	}
	publication, err := newPublication(
		repository.Name, servicecatalog.SourceAnalysisUnitV1, "",
		repository.IndexedCommitHash, unit.Digest, catalog, canonical,
		catalogDigest, census,
	)
	if err != nil {
		return "", err
	}
	if err := r.withMutation(ctx, repository.Name, publication, func() error {
		if err := r.Store.PublishServiceCatalog(ctx, publication); err != nil {
			return err
		}
		return r.reconcilePublishedServiceStates(ctx, repository.Name)
	}); err != nil {
		return "", err
	}
	return OutcomeLegacyImported, nil
}

func (r *Reconciler) withMutation(
	ctx context.Context,
	repository string,
	publication servicecatalog.Publication,
	mutate func() error,
) error {
	if r.WithMutation == nil {
		return mutate()
	}
	return r.WithMutation(ctx, repository, publication, mutate)
}

func (r *Reconciler) reconcilePublishedServiceStates(
	ctx context.Context,
	repository string,
) error {
	publication, err := r.Store.GetServiceCatalog(ctx, repository)
	if err != nil {
		return fmt.Errorf("strict-open catalog for service state: %w", err)
	}
	if err := r.Store.ReconcileServiceStates(ctx, *publication); err != nil {
		return fmt.Errorf("reconcile service state: %w", err)
	}
	return nil
}

func legacyCatalog(unit *analysisunit.State) servicecatalog.Catalog {
	memberships := make([]servicecatalog.Membership, 0,
		len(unit.PrimaryPaths)+len(unit.SupportingPaths)+1)
	for _, selectedPath := range unit.PrimaryPaths {
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: unit.Name, Path: selectedPath,
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	for _, selectedPath := range unit.SupportingPaths {
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: unit.Name, Path: selectedPath,
			Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase,
		})
	}
	if unit.TypedIndex != nil {
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: unit.Name, Path: unit.TypedIndex.Path,
			Role: servicecatalog.RoleTyped, Origin: servicecatalog.OriginBase,
		})
	}
	return servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind:    servicecatalog.AuthorityOperator,
			ID:      servicecatalog.AnalysisUnitV1AuthorityID,
			Version: unit.Digest,
		},
		Services: []servicecatalog.Service{{
			Key: unit.Name, DisplayName: unit.Name,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: memberships,
		Unowned:     []servicecatalog.UnownedPlacement{},
	}
}

type censusResult struct {
	Digest            string
	FileCount         int
	AcceptedFileCount int
	UnownedFileCount  int
	LegacyUnowned     []servicecatalog.UnownedPlacement
}

func (r *Reconciler) census(
	ctx context.Context,
	repository, commit string,
	catalog servicecatalog.Catalog,
	fillLegacyUnowned bool,
) (censusResult, error) {
	return r.censusValidated(
		ctx, repository, commit, catalog, fillLegacyUnowned,
		func(candidate servicecatalog.Catalog) error {
			_, err := servicecatalog.Normalize(candidate)
			return err
		},
	)
}

func (r *Reconciler) censusValidated(
	ctx context.Context,
	repository, commit string,
	catalog servicecatalog.Catalog,
	fillLegacyUnowned bool,
	validate func(servicecatalog.Catalog) error,
) (censusResult, error) {
	if err := validate(catalog); err != nil {
		return censusResult{}, fmt.Errorf("validate catalog before census: %w", err)
	}
	dir, err := phebssync.SafeRepoDir(r.DataDir, repository)
	if err != nil {
		return censusResult{}, err
	}
	placements := newPlacements(catalog)
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := gitobj.Command(childCtx, dir, "ls-tree", "-rz", "--full-tree", commit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return censusResult{}, fmt.Errorf("open source census: %w", err)
	}
	var stderr gitobj.StderrBuffer
	cmd.Stderr = &stderr
	handle, err := dispatchadmission.StartProduction(childCtx, dispatchadmission.SiteServiceCatalogCensus, cmd)
	if err != nil {
		return censusResult{}, fmt.Errorf("start source census: %w", err)
	}
	result := censusResult{LegacyUnowned: []servicecatalog.UnownedPlacement{}}
	baseDistinctPaths := placements.distinctCount()
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-source-census-v1\x00"))
	reader := bufio.NewReader(stdout)
	parseErr := error(nil)
	for {
		record, done, readErr := readNULRecord(reader, repopath.MaxBytes+160)
		if readErr != nil {
			parseErr = readErr
			break
		}
		if done {
			break
		}
		mode, objectType, objectID, filePath, recordErr := parseTreeRecord(record)
		if recordErr != nil {
			parseErr = recordErr
			break
		}
		if objectType != "blob" || mode != "100644" && mode != "100755" {
			continue
		}
		if err := repopath.Validate(filePath); err != nil {
			parseErr = fmt.Errorf("source census contains a non-canonical regular path")
			break
		}
		result.FileCount++
		_, _ = hash.Write([]byte(mode))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(objectID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(filePath))
		_, _ = hash.Write([]byte{0})
		accepted, unowned := placements.classify(filePath)
		if accepted && unowned {
			parseErr = fmt.Errorf("source path %q is both accepted and unowned", filePath)
			break
		}
		if !accepted && !unowned && fillLegacyUnowned {
			if baseDistinctPaths+len(result.LegacyUnowned) >=
				servicecatalog.MaxDistinctPaths {
				parseErr = servicecatalog.ErrDistinctPathLimit
				break
			}
			result.LegacyUnowned = append(result.LegacyUnowned,
				servicecatalog.UnownedPlacement{
					Path: filePath, Origin: servicecatalog.OriginBase,
				})
			unowned = true
		}
		if !accepted && !unowned {
			parseErr = fmt.Errorf("source path %q is absent from the accepted/unowned complement", filePath)
			break
		}
		if accepted {
			result.AcceptedFileCount++
		} else {
			result.UnownedFileCount++
		}
	}
	if parseErr != nil {
		cancel()
	}
	waitErr := handle.Wait()
	if parseErr != nil {
		return censusResult{}, fmt.Errorf("stream source census: %w", parseErr)
	}
	if waitErr != nil {
		return censusResult{}, gitobj.WrapError(
			ctx, []string{"ls-tree", "-rz", "--full-tree", commit},
			waitErr, stderr.String(),
		)
	}
	if missing := placements.missing(); missing != "" {
		return censusResult{}, fmt.Errorf("catalog placement %q is absent from the source census", missing)
	}
	result.Digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

type placementIndex struct {
	all      map[string]bool
	accepted map[string]bool
	unowned  map[string]bool
}

func newPlacements(catalog servicecatalog.Catalog) *placementIndex {
	services := make(map[string]string, len(catalog.Services))
	for _, service := range catalog.Services {
		services[service.Key] = service.Disposition
	}
	index := &placementIndex{
		all: make(map[string]bool), accepted: make(map[string]bool),
		unowned: make(map[string]bool),
	}
	for _, membership := range catalog.Memberships {
		index.all[membership.Path] = false
		if services[membership.ServiceKey] == servicecatalog.DispositionAccepted {
			index.accepted[membership.Path] = false
		}
	}
	for _, unowned := range catalog.Unowned {
		index.unowned[unowned.Path] = false
	}
	return index
}

func (p *placementIndex) classify(filePath string) (accepted, unowned bool) {
	for current := filePath; ; current = path.Dir(current) {
		if _, exists := p.all[current]; exists {
			p.all[current] = true
		}
		if _, exists := p.accepted[current]; exists {
			p.accepted[current] = true
			accepted = true
		}
		if _, exists := p.unowned[current]; exists {
			p.unowned[current] = true
			unowned = true
		}
		if current == "." || !strings.Contains(current, "/") {
			break
		}
	}
	return accepted, unowned
}

func (p *placementIndex) missing() string {
	missing := make([]string, 0)
	for value, hit := range p.all {
		if !hit {
			missing = append(missing, value)
		}
	}
	for value, hit := range p.unowned {
		if !hit {
			missing = append(missing, value)
		}
	}
	slices.Sort(missing)
	if len(missing) == 0 {
		return ""
	}
	return missing[0]
}

func (p *placementIndex) distinctCount() int {
	paths := make(map[string]struct{}, len(p.all)+len(p.unowned))
	for value := range p.all {
		paths[value] = struct{}{}
	}
	for value := range p.unowned {
		paths[value] = struct{}{}
	}
	return len(paths)
}

func readCatalog(selection config.ServiceCatalog) ([]byte, error) {
	content, err := readSelectedFile(selection.Path, servicecatalog.MaxEncodedBytes)
	if errors.Is(err, errSelectedCatalogEncodedLimit) {
		return nil, servicecatalog.ErrEncodedLimit
	}
	return content, err
}

var errSelectedCatalogEncodedLimit = errors.New("selected catalog exceeds the encoded-byte limit")

func readSelectedFile(selectedPath string, limit int64) ([]byte, error) {
	before, err := os.Lstat(selectedPath)
	if err != nil {
		return nil, fmt.Errorf("inspect selected catalog: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("selected catalog must be a non-symlink regular file")
	}
	if before.Size() > limit {
		return nil, errSelectedCatalogEncodedLimit
	}
	file, err := os.Open(selectedPath)
	if err != nil {
		return nil, fmt.Errorf("open selected catalog: %w", err)
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("selected catalog changed while opening")
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read selected catalog: %w", err)
	}
	if int64(len(content)) > limit {
		return nil, errSelectedCatalogEncodedLimit
	}
	return content, nil
}

func newPublication(
	repository, sourceKind, sourcePath, sourceCommit, legacyDigest string,
	catalog servicecatalog.Catalog,
	canonical []byte,
	catalogDigest string,
	census censusResult,
) (servicecatalog.Publication, error) {
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: sourceKind, SourcePath: sourcePath,
		SourceCommit: sourceCommit, SourceCensusDigest: census.Digest,
		SourceFileCount:   census.FileCount,
		AcceptedFileCount: census.AcceptedFileCount,
		UnownedFileCount:  census.UnownedFileCount,
		Authority:         catalog.Authority, Override: catalog.Override,
		CatalogDigest:            catalogDigest,
		LegacyAnalysisUnitDigest: legacyDigest,
		Canonical:                canonical,
	}
	generation, err := servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		return servicecatalog.Publication{}, err
	}
	publication.GenerationDigest = generation
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		return servicecatalog.Publication{}, err
	}
	return publication, nil
}

func readNULRecord(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	var record []byte
	for {
		fragment, err := reader.ReadSlice(0)
		if len(record)+len(fragment) > limit {
			return nil, false, errors.New("source census record exceeds the path bound")
		}
		record = append(record, fragment...)
		switch {
		case err == nil:
			return record[:len(record)-1], false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(record) == 0:
			return nil, true, nil
		case errors.Is(err, io.EOF):
			return nil, false, errors.New("source census ended inside a record")
		default:
			return nil, false, err
		}
	}
}

func parseTreeRecord(record []byte) (mode, objectType, objectID, filePath string, err error) {
	metadata, rawPath, ok := bytes.Cut(record, []byte{'\t'})
	fields := bytes.Fields(metadata)
	if !ok || len(fields) != 3 {
		return "", "", "", "", errors.New("source census emitted a malformed record")
	}
	mode, objectType, objectID, filePath =
		string(fields[0]), string(fields[1]), string(fields[2]), string(rawPath)
	if len(objectID) != 40 && len(objectID) != 64 {
		return "", "", "", "", errors.New("source census emitted a malformed object ID")
	}
	if decoded, decodeErr := hex.DecodeString(objectID); decodeErr != nil ||
		hex.EncodeToString(decoded) != objectID {
		return "", "", "", "", errors.New("source census emitted a malformed object ID")
	}
	return mode, objectType, objectID, filePath, nil
}

func (r *Report) add(outcome Outcome) {
	switch outcome {
	case OutcomeCurrent:
		r.Current++
	case OutcomePublished:
		r.Published++
	case OutcomeLegacyImported:
		r.LegacyImported++
	case OutcomeNotReady:
		r.NotReady++
	case OutcomeUnselected:
		r.Unselected++
	}
}
