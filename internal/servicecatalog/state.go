package servicecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	ServiceStateSchema      = "phebs-service-state-v1"
	RepositoryStateSchema   = "phebs-service-state-repository-v1"
	DesiredServiceSchema    = "phebs-service-desired-v1"
	DesiredGenerationSchema = "phebs-service-desired-generation-v1"
	SourceGenerationSchema  = "phebs-service-source-generation-v1"

	StatusCurrent     = "current"
	StatusStale       = "stale"
	StatusUnavailable = "unavailable"
	StatusConflict    = "conflict"
	StatusRemoved     = "removed"

	ImplicitRemovalReason = "removed from the selected catalog"
)

var ErrInvalidServiceState = errors.New("invalid service state")

// ServiceProjection is the exact service-local record input derived from one
// complete catalog publication. Its digest deliberately excludes unrelated
// catalog records; ServiceDesiredGeneration adds the store-minted incarnation.
type ServiceProjection struct {
	Repository        string
	Service           Service
	Memberships       []Membership
	SourceGeneration  string
	CatalogGeneration string
	GenerationDigest  string
	Removed           bool
}

// ServiceState is the one current lifecycle row for a repository-local service
// key. Incarnation advances only when a removed key is reintroduced. Active
// identities are retained across stale/conflict/removal transitions, but are
// cleared before a new incarnation can become active.
type ServiceState struct {
	Schema                   string
	Repository               string
	ServiceKey               string
	DisplayName              string
	Disposition              string
	Origin                   string
	Reason                   string
	Successors               []string
	Incarnation              uint64
	DesiredGeneration        string
	DesiredSourceGeneration  string
	DesiredCatalogGeneration string
	ActiveDesiredGeneration  string
	ActiveSourceGeneration   string
	ActiveCatalogGeneration  string
	ActiveSearchGeneration   string
	Status                   string
	Removed                  bool
	StateDigest              string
	ControlRevision          uint64
	ChangedAt                time.Time
}

// RepositoryState is the bounded current summary. Counts are maintained by
// transitions over at most the admitted current catalog plus its live rows;
// they never require a scan of immutable catalog generations or old tombstones.
type RepositoryState struct {
	Schema                 string
	Repository             string
	CatalogGeneration      string
	CatalogControlRevision uint64
	CatalogServiceCount    int
	LiveServiceCount       int
	CurrentCount           int
	StaleCount             int
	UnavailableCount       int
	ConflictCount          int
	TombstoneCount         int
	SummaryDigest          string
	ControlRevision        uint64
	UpdatedAt              time.Time
}

// ServiceActivation is a compare-and-swap request to make one accepted desired
// generation active. Both row and repository revisions prevent a stale worker
// from publishing after a catalog or sibling transition.
type ServiceActivation struct {
	Repository                string
	ServiceKey                string
	Incarnation               uint64
	DesiredGeneration         string
	StateControlRevision      uint64
	RepositoryControlRevision uint64
}

// SourceGenerationDigest binds the repository, exact indexed revision, and
// streamed census without importing the whole catalog identity.
func SourceGenerationDigest(publication Publication) (string, error) {
	persisted := publication.ControlRevision != 0 || !publication.PublishedAt.IsZero()
	verified, err := VerifyPublication(publication, persisted)
	if err != nil {
		return "", err
	}
	return sourceGenerationDigest(verified.publication)
}

func sourceGenerationDigest(publication Publication) (string, error) {
	binding := struct {
		Schema             string `json:"schema"`
		Repository         string `json:"repository"`
		SourceCommit       string `json:"source_commit"`
		SourceCensusDigest string `json:"source_census_digest"`
		SourceFileCount    int    `json:"source_file_count"`
	}{
		Schema: SourceGenerationSchema, Repository: publication.Repository,
		SourceCommit:       publication.SourceCommit,
		SourceCensusDigest: publication.SourceCensusDigest,
		SourceFileCount:    publication.SourceFileCount,
	}
	return digestJSON("phebs-service-source-generation-v1\x00", binding)
}

// ProjectServices derives one sorted, exact desired projection per catalog
// service. Memberships for other services and unowned repository paths cannot
// change a projection digest.
func ProjectServices(publication Publication) ([]ServiceProjection, error) {
	persisted := publication.ControlRevision != 0 || !publication.PublishedAt.IsZero()
	verified, err := VerifyPublication(publication, persisted)
	if err != nil {
		return nil, err
	}
	return verified.ProjectServices()
}

// ProjectServices derives every service projection from one verified catalog
// decode. It is intended for bounded reconciliation, not point reads.
func (verified VerifiedPublication) ProjectServices() ([]ServiceProjection, error) {
	if !verified.verified {
		return nil, publicationInvalidf("publication was not verified")
	}
	sourceGeneration, err := sourceGenerationDigest(verified.publication)
	if err != nil {
		return nil, err
	}
	projections := make([]ServiceProjection, 0, len(verified.catalog.Services))
	for _, service := range verified.catalog.Services {
		projection, digestErr := projectService(
			verified.publication, sourceGeneration, service,
			membershipsForService(verified.catalog.Memberships, service.Key),
		)
		if digestErr != nil {
			return nil, digestErr
		}
		projections = append(projections, projection)
	}
	return projections, nil
}

// ProjectService strict-opens one publication and derives only the requested
// service projection. It validates the complete admitted catalog, but it does
// not allocate or hash sibling projections.
func ProjectService(
	publication Publication,
	serviceKey string,
) (ServiceProjection, bool, error) {
	persisted := publication.ControlRevision != 0 || !publication.PublishedAt.IsZero()
	verified, err := VerifyPublication(publication, persisted)
	if err != nil {
		return ServiceProjection{}, false, err
	}
	return verified.ProjectService(serviceKey)
}

// ProjectService derives one service-local projection from an already strict-
// opened publication without decoding or hashing any sibling projection.
func (verified VerifiedPublication) ProjectService(
	serviceKey string,
) (ServiceProjection, bool, error) {
	if !verified.verified {
		return ServiceProjection{}, false, publicationInvalidf("publication was not verified")
	}
	index, found := slices.BinarySearchFunc(
		verified.catalog.Services, serviceKey,
		func(service Service, key string) int {
			switch {
			case service.Key < key:
				return -1
			case service.Key > key:
				return 1
			default:
				return 0
			}
		},
	)
	if !found {
		return ServiceProjection{}, false, nil
	}
	sourceGeneration, err := sourceGenerationDigest(verified.publication)
	if err != nil {
		return ServiceProjection{}, false, err
	}
	projection, err := projectService(
		verified.publication, sourceGeneration, verified.catalog.Services[index],
		membershipsForService(verified.catalog.Memberships, serviceKey),
	)
	if err != nil {
		return ServiceProjection{}, false, err
	}
	return projection, true, nil
}

func membershipsForService(memberships []Membership, serviceKey string) []Membership {
	start := sort.Search(len(memberships), func(index int) bool {
		return memberships[index].ServiceKey >= serviceKey
	})
	if start == len(memberships) || memberships[start].ServiceKey != serviceKey {
		return nil
	}
	end := start + sort.Search(len(memberships)-start, func(index int) bool {
		return memberships[start+index].ServiceKey > serviceKey
	})
	return slices.Clone(memberships[start:end])
}

func projectService(
	publication Publication,
	sourceGeneration string,
	service Service,
	memberships []Membership,
) (ServiceProjection, error) {
	service.Successors = slices.Clone(service.Successors)
	binding := struct {
		Schema           string       `json:"schema"`
		Repository       string       `json:"repository"`
		SourceGeneration string       `json:"source_generation"`
		Service          Service      `json:"service"`
		Memberships      []Membership `json:"memberships"`
	}{
		Schema: DesiredServiceSchema, Repository: publication.Repository,
		SourceGeneration: sourceGeneration, Service: service,
		Memberships: memberships,
	}
	generation, err := digestJSON("phebs-service-desired-v1\x00", binding)
	if err != nil {
		return ServiceProjection{}, err
	}
	return ServiceProjection{
		Repository: publication.Repository, Service: service,
		Memberships: memberships, SourceGeneration: sourceGeneration,
		CatalogGeneration: publication.GenerationDigest,
		GenerationDigest:  generation,
		Removed:           service.Disposition == DispositionRejected,
	}, nil
}

// ServiceDesiredGeneration combines the isolated service projection with its
// current incarnation. Removal/re-add can therefore never reuse the prior
// incarnation's desired or active identity even when source and record bytes
// return to the same semantic endpoint.
func ServiceDesiredGeneration(
	projection ServiceProjection,
	incarnation uint64,
) (string, error) {
	if incarnation == 0 || !validSHA256Digest(projection.GenerationDigest) ||
		!validSHA256Digest(projection.SourceGeneration) ||
		!validSHA256Digest(projection.CatalogGeneration) ||
		projection.Repository == "" || !validServiceKey(projection.Service.Key) {
		return "", stateInvalidf("desired generation input is incomplete")
	}
	binding := struct {
		Schema           string `json:"schema"`
		Repository       string `json:"repository"`
		ServiceKey       string `json:"service_key"`
		Incarnation      uint64 `json:"incarnation"`
		ProjectionDigest string `json:"projection_digest"`
	}{
		Schema: DesiredGenerationSchema, Repository: projection.Repository,
		ServiceKey: projection.Service.Key, Incarnation: incarnation,
		ProjectionDigest: projection.GenerationDigest,
	}
	return digestJSON("phebs-service-desired-generation-v1\x00", binding)
}

// ValidateServiceState proves the closed row shape and its semantic digest.
func ValidateServiceState(state ServiceState, persisted bool) error {
	if state.Schema != ServiceStateSchema {
		return stateInvalidf("schema must be %q", ServiceStateSchema)
	}
	if err := reponame.Validate(state.Repository); err != nil {
		return stateInvalidf("repository is not canonical")
	}
	service := Service{
		Key: state.ServiceKey, DisplayName: state.DisplayName,
		Disposition: state.Disposition, Origin: state.Origin,
		Reason: state.Reason, Successors: slices.Clone(state.Successors),
	}
	if err := validateService(service, state.Origin == OriginOverride); err != nil {
		return stateInvalidf("service record: %v", err)
	}
	if !slices.IsSorted(state.Successors) {
		return stateInvalidf("successors are not sorted")
	}
	for index, successor := range state.Successors {
		if !validServiceKey(successor) {
			return stateInvalidf("invalid successor %q", successor)
		}
		if successor == state.ServiceKey {
			return stateInvalidf("service is its own successor")
		}
		if index > 0 && state.Successors[index-1] == successor {
			return stateInvalidf("duplicate successor %q", successor)
		}
	}
	if state.Incarnation == 0 {
		return stateInvalidf("incarnation is required")
	}
	desiredAny := state.DesiredGeneration != "" || state.DesiredSourceGeneration != "" ||
		state.DesiredCatalogGeneration != ""
	desiredAll := validSHA256Digest(state.DesiredGeneration) &&
		validSHA256Digest(state.DesiredSourceGeneration) &&
		validSHA256Digest(state.DesiredCatalogGeneration)
	if desiredAny != desiredAll {
		return stateInvalidf("desired identities must be all present or all absent")
	}
	activeAny := state.ActiveDesiredGeneration != "" || state.ActiveSourceGeneration != "" ||
		state.ActiveCatalogGeneration != ""
	activeAll := validSHA256Digest(state.ActiveDesiredGeneration) &&
		validSHA256Digest(state.ActiveSourceGeneration) &&
		validSHA256Digest(state.ActiveCatalogGeneration)
	if activeAny != activeAll {
		return stateInvalidf("active identities must be all present or all absent")
	}
	if state.ActiveSearchGeneration != "" &&
		(!activeAll || !validSHA256Digest(state.ActiveSearchGeneration)) {
		return stateInvalidf("active search identity requires valid active identities")
	}
	if state.Removed {
		if state.Status != StatusRemoved || state.Disposition != DispositionRejected {
			return stateInvalidf("removed tombstone requires rejected/removed state")
		}
	} else {
		switch state.Status {
		case StatusCurrent:
			if state.Disposition != DispositionAccepted || !activeAll ||
				state.ActiveDesiredGeneration != state.DesiredGeneration ||
				state.ActiveSourceGeneration != state.DesiredSourceGeneration {
				return stateInvalidf("current state does not match accepted desired identity")
			}
		case StatusStale:
			if state.Disposition != DispositionAccepted || !activeAll ||
				state.ActiveDesiredGeneration == state.DesiredGeneration {
				return stateInvalidf("stale state requires a different active identity")
			}
		case StatusUnavailable:
			if state.Disposition != DispositionAccepted && state.Disposition != DispositionProposal {
				return stateInvalidf("unavailable state requires accepted or proposal disposition")
			}
		case StatusConflict:
			if state.Disposition != DispositionConflict {
				return stateInvalidf("conflict state requires conflict disposition")
			}
		default:
			return stateInvalidf("unsupported status %q", state.Status)
		}
		if !desiredAll {
			return stateInvalidf("live state requires desired identities")
		}
	}
	digest, err := serviceStateDigest(state)
	if err != nil {
		return err
	}
	if state.StateDigest != digest {
		return stateInvalidf("state digest is inconsistent")
	}
	if persisted && (state.ControlRevision == 0 || state.ChangedAt.IsZero()) {
		return stateInvalidf("persisted state requires revision and time")
	}
	if !persisted && (state.ControlRevision != 0 || !state.ChangedAt.IsZero()) {
		return stateInvalidf("unpersisted state cannot mint revision or time")
	}
	return nil
}

// SetServiceStateDigest derives the semantic state digest after callers have
// populated every field except revision/time.
func SetServiceStateDigest(state *ServiceState) error {
	if state == nil {
		return stateInvalidf("state is nil")
	}
	state.Successors = append([]string{}, state.Successors...)
	digest, err := serviceStateDigest(*state)
	if err != nil {
		return err
	}
	state.StateDigest = digest
	return nil
}

// ValidateRepositoryState proves the bounded summary and its digest.
func ValidateRepositoryState(state RepositoryState, persisted bool) error {
	if state.Schema != RepositoryStateSchema {
		return stateInvalidf("repository schema must be %q", RepositoryStateSchema)
	}
	if err := reponame.Validate(state.Repository); err != nil {
		return stateInvalidf("summary repository is not canonical")
	}
	if !validSHA256Digest(state.CatalogGeneration) || state.CatalogControlRevision == 0 {
		return stateInvalidf("summary catalog identity is incomplete")
	}
	counts := []int{
		state.CatalogServiceCount, state.LiveServiceCount, state.CurrentCount,
		state.StaleCount, state.UnavailableCount, state.ConflictCount,
		state.TombstoneCount,
	}
	for _, count := range counts {
		if count < 0 {
			return stateInvalidf("summary count is negative")
		}
	}
	if state.CatalogServiceCount > MaxServices || state.LiveServiceCount > MaxServices ||
		state.LiveServiceCount != state.CurrentCount+state.StaleCount+
			state.UnavailableCount+state.ConflictCount ||
		state.CatalogServiceCount < state.LiveServiceCount ||
		state.TombstoneCount < state.CatalogServiceCount-state.LiveServiceCount {
		return stateInvalidf("summary counts are inconsistent")
	}
	digest, err := repositoryStateDigest(state)
	if err != nil {
		return err
	}
	if state.SummaryDigest != digest {
		return stateInvalidf("summary digest is inconsistent")
	}
	if persisted && (state.ControlRevision == 0 || state.UpdatedAt.IsZero()) {
		return stateInvalidf("persisted summary requires revision and time")
	}
	if !persisted && (state.ControlRevision != 0 || !state.UpdatedAt.IsZero()) {
		return stateInvalidf("unpersisted summary cannot mint revision or time")
	}
	return nil
}

func SetRepositoryStateDigest(state *RepositoryState) error {
	if state == nil {
		return stateInvalidf("summary is nil")
	}
	digest, err := repositoryStateDigest(*state)
	if err != nil {
		return err
	}
	state.SummaryDigest = digest
	return nil
}

func serviceStateDigest(state ServiceState) (string, error) {
	if len(state.DisplayName) > MaxDisplayNameBytes || len(state.Reason) > MaxReasonBytes ||
		!utf8.ValidString(state.DisplayName) || !utf8.ValidString(state.Reason) ||
		len(state.Successors) > MaxSuccessorEdges {
		return "", stateInvalidf("state text or successor limit exceeded")
	}
	binding := struct {
		Schema                   string   `json:"schema"`
		Repository               string   `json:"repository"`
		ServiceKey               string   `json:"service_key"`
		DisplayName              string   `json:"display_name"`
		Disposition              string   `json:"disposition"`
		Origin                   string   `json:"origin"`
		Reason                   string   `json:"reason,omitempty"`
		Successors               []string `json:"successors"`
		Incarnation              uint64   `json:"incarnation"`
		DesiredGeneration        string   `json:"desired_generation,omitempty"`
		DesiredSourceGeneration  string   `json:"desired_source_generation,omitempty"`
		DesiredCatalogGeneration string   `json:"desired_catalog_generation,omitempty"`
		ActiveDesiredGeneration  string   `json:"active_desired_generation,omitempty"`
		ActiveSourceGeneration   string   `json:"active_source_generation,omitempty"`
		ActiveCatalogGeneration  string   `json:"active_catalog_generation,omitempty"`
		ActiveSearchGeneration   string   `json:"active_search_generation,omitempty"`
		Status                   string   `json:"status"`
		Removed                  bool     `json:"removed"`
	}{
		Schema: state.Schema, Repository: state.Repository, ServiceKey: state.ServiceKey,
		DisplayName: state.DisplayName, Disposition: state.Disposition, Origin: state.Origin,
		Reason: state.Reason, Successors: append([]string{}, state.Successors...),
		Incarnation:              state.Incarnation,
		DesiredGeneration:        state.DesiredGeneration,
		DesiredSourceGeneration:  state.DesiredSourceGeneration,
		DesiredCatalogGeneration: state.DesiredCatalogGeneration,
		ActiveDesiredGeneration:  state.ActiveDesiredGeneration,
		ActiveSourceGeneration:   state.ActiveSourceGeneration,
		ActiveCatalogGeneration:  state.ActiveCatalogGeneration,
		ActiveSearchGeneration:   state.ActiveSearchGeneration,
		Status:                   state.Status, Removed: state.Removed,
	}
	return digestJSON("phebs-service-state-v1\x00", binding)
}

func repositoryStateDigest(state RepositoryState) (string, error) {
	binding := struct {
		Schema                 string `json:"schema"`
		Repository             string `json:"repository"`
		CatalogGeneration      string `json:"catalog_generation"`
		CatalogControlRevision uint64 `json:"catalog_control_revision"`
		CatalogServiceCount    int    `json:"catalog_service_count"`
		LiveServiceCount       int    `json:"live_service_count"`
		CurrentCount           int    `json:"current_count"`
		StaleCount             int    `json:"stale_count"`
		UnavailableCount       int    `json:"unavailable_count"`
		ConflictCount          int    `json:"conflict_count"`
		TombstoneCount         int    `json:"tombstone_count"`
	}{
		Schema: state.Schema, Repository: state.Repository,
		CatalogGeneration:      state.CatalogGeneration,
		CatalogControlRevision: state.CatalogControlRevision,
		CatalogServiceCount:    state.CatalogServiceCount,
		LiveServiceCount:       state.LiveServiceCount,
		CurrentCount:           state.CurrentCount, StaleCount: state.StaleCount,
		UnavailableCount: state.UnavailableCount, ConflictCount: state.ConflictCount,
		TombstoneCount: state.TombstoneCount,
	}
	return digestJSON("phebs-service-state-repository-v1\x00", binding)
}

func digestJSON(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: canonical encoding: %v", ErrInvalidServiceState, err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func stateInvalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidServiceState, fmt.Sprintf(format, args...))
}
