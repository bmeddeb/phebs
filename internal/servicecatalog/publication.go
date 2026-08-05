package servicecatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	PublicationSchema = "phebs-service-catalog-publication-v1"

	SourceCommitted      = "committed"
	SourceOperator       = "operator"
	SourceAnalysisUnitV1 = "analysis-unit-v1"

	AnalysisUnitV1AuthorityID = "analysis-unit-v1"
)

var ErrInvalidPublication = errors.New("invalid service catalog publication")

// Publication is one immutable, complete catalog generation together with
// its current-pointer fence. Canonical contains the exact T33.1 canonical JSON
// and remains precious database state; it is not a rebuildable filesystem
// artifact.
type Publication struct {
	Schema                   string
	Repository               string
	SourceKind               string
	SourcePath               string
	SourceCommit             string
	SourceCensusDigest       string
	SourceFileCount          int
	AcceptedFileCount        int
	UnownedFileCount         int
	Authority                Authority
	Override                 *OperatorOverride
	CatalogDigest            string
	LegacyAnalysisUnitDigest string
	GenerationDigest         string
	Canonical                []byte
	ControlRevision          uint64
	PublishedAt              time.Time
}

// VerifiedPublication is an opaque strict-open publication and its already
// decoded catalog. Service-local readers may reuse it without parsing the
// admitted catalog again; its unexported fields prevent callers from forging a
// validation result.
type VerifiedPublication struct {
	publication Publication
	catalog     Catalog
	verified    bool
}

// ValidatePublication proves that every stored scalar agrees with the strict
// canonical catalog and with the domain-separated generation identity.
func ValidatePublication(publication Publication, persisted bool) error {
	_, err := VerifyPublication(publication, persisted)
	return err
}

// VerifyPublication performs strict validation once and returns an opaque view
// that can derive one or many service projections from the decoded catalog.
func VerifyPublication(
	publication Publication,
	persisted bool,
) (VerifiedPublication, error) {
	var catalog Catalog
	if err := validatePublication(publication, persisted, &catalog); err != nil {
		return VerifiedPublication{}, err
	}
	return VerifiedPublication{
		publication: publication,
		catalog:     catalog,
		verified:    true,
	}, nil
}

func validatePublication(
	publication Publication,
	persisted bool,
	opened *Catalog,
) error {
	if publication.Schema != PublicationSchema {
		return publicationInvalidf("schema must be %q", PublicationSchema)
	}
	if err := reponame.Validate(publication.Repository); err != nil {
		return publicationInvalidf("repository is not canonical")
	}
	if !isFullGitObjectID(publication.SourceCommit) {
		return publicationInvalidf("source commit must be a full Git object ID")
	}
	switch publication.SourceKind {
	case SourceCommitted:
		if publication.SourcePath == "" || !filepath.IsAbs(publication.SourcePath) ||
			filepath.Clean(publication.SourcePath) != publication.SourcePath {
			return publicationInvalidf("committed source path must be absolute and clean")
		}
		if publication.LegacyAnalysisUnitDigest != "" {
			return publicationInvalidf("committed source cannot carry a legacy digest")
		}
	case SourceOperator:
		if publication.SourcePath == "" || !filepath.IsAbs(publication.SourcePath) ||
			filepath.Clean(publication.SourcePath) != publication.SourcePath {
			return publicationInvalidf("operator source path must be absolute and clean")
		}
		if publication.LegacyAnalysisUnitDigest != "" {
			return publicationInvalidf("operator source cannot carry a legacy digest")
		}
	case SourceAnalysisUnitV1:
		if publication.SourcePath != "" {
			return publicationInvalidf("analysis-unit-v1 source path must be empty")
		}
		if !validSHA256Digest(publication.LegacyAnalysisUnitDigest) {
			return publicationInvalidf("analysis-unit-v1 digest must be canonical sha256")
		}
	default:
		return publicationInvalidf("unsupported source kind %q", publication.SourceKind)
	}
	if !validSHA256Digest(publication.SourceCensusDigest) {
		return publicationInvalidf("source census digest must be canonical sha256")
	}
	if publication.SourceFileCount < 0 || publication.AcceptedFileCount < 0 ||
		publication.UnownedFileCount < 0 ||
		publication.AcceptedFileCount+publication.UnownedFileCount != publication.SourceFileCount {
		return publicationInvalidf("source census counts are inconsistent")
	}
	if len(publication.Canonical) > MaxCanonicalBytes {
		return ErrCanonicalLimit
	}
	catalog, err := Decode(publication.Canonical)
	if err != nil {
		return fmt.Errorf("%w: canonical catalog: %v", ErrInvalidPublication, err)
	}
	canonical, err := Canonical(catalog)
	if err != nil {
		return fmt.Errorf("%w: canonical catalog: %v", ErrInvalidPublication, err)
	}
	if !bytes.Equal(canonical, publication.Canonical) {
		return publicationInvalidf("catalog bytes are not canonical")
	}
	digest, err := Digest(catalog)
	if err != nil {
		return fmt.Errorf("%w: catalog digest: %v", ErrInvalidPublication, err)
	}
	if digest != publication.CatalogDigest || catalog.Authority != publication.Authority ||
		!equalOverride(catalog.Override, publication.Override) {
		return publicationInvalidf("catalog identity fields disagree with canonical bytes")
	}
	if publication.SourceKind == SourceCommitted &&
		(publication.Authority.Kind != AuthorityCommitted ||
			publication.Authority.Version != publication.SourceCommit) {
		return publicationInvalidf("committed authority is not fenced to the source commit")
	}
	if publication.SourceKind == SourceOperator && publication.Authority.Kind != AuthorityOperator {
		return publicationInvalidf("operator source requires operator authority")
	}
	if publication.SourceKind == SourceAnalysisUnitV1 &&
		(publication.Authority.Kind != AuthorityOperator ||
			publication.Authority.ID != AnalysisUnitV1AuthorityID ||
			publication.Authority.Version != publication.LegacyAnalysisUnitDigest) {
		return publicationInvalidf("analysis-unit-v1 authority does not preserve its digest")
	}
	generationDigest, err := PublicationGenerationDigest(publication)
	if err != nil {
		return err
	}
	if generationDigest != publication.GenerationDigest {
		return publicationInvalidf("generation digest is inconsistent")
	}
	if persisted && (publication.ControlRevision == 0 || publication.PublishedAt.IsZero()) {
		return publicationInvalidf("persisted publication requires revision and time")
	}
	if !persisted && (publication.ControlRevision != 0 || !publication.PublishedAt.IsZero()) {
		return publicationInvalidf("unpublished input cannot mint revision or time")
	}
	if opened != nil {
		*opened = catalog
	}
	return nil
}

// PublicationGenerationDigest binds the repository, exact source census,
// selected source provenance, legacy namespace when present, and canonical
// catalog identity. Publication time/revision remain separate ABA fences.
func PublicationGenerationDigest(publication Publication) (string, error) {
	binding := struct {
		Schema                   string            `json:"schema"`
		Repository               string            `json:"repository"`
		SourceKind               string            `json:"source_kind"`
		SourcePath               string            `json:"source_path"`
		SourceCommit             string            `json:"source_commit"`
		SourceCensusDigest       string            `json:"source_census_digest"`
		SourceFileCount          int               `json:"source_file_count"`
		AcceptedFileCount        int               `json:"accepted_file_count"`
		UnownedFileCount         int               `json:"unowned_file_count"`
		Authority                Authority         `json:"authority"`
		Override                 *OperatorOverride `json:"override,omitempty"`
		CatalogDigest            string            `json:"catalog_digest"`
		LegacyAnalysisUnitDigest string            `json:"legacy_analysis_unit_digest,omitempty"`
	}{
		Schema: PublicationSchema, Repository: publication.Repository,
		SourceKind: publication.SourceKind, SourcePath: publication.SourcePath,
		SourceCommit:       publication.SourceCommit,
		SourceCensusDigest: publication.SourceCensusDigest,
		SourceFileCount:    publication.SourceFileCount,
		AcceptedFileCount:  publication.AcceptedFileCount,
		UnownedFileCount:   publication.UnownedFileCount,
		Authority:          publication.Authority, Override: cloneOverride(publication.Override),
		CatalogDigest:            publication.CatalogDigest,
		LegacyAnalysisUnitDigest: publication.LegacyAnalysisUnitDigest,
	}
	canonical, err := json.Marshal(binding)
	if err != nil {
		return "", fmt.Errorf("%w: encode generation binding: %v", ErrInvalidPublication, err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-catalog-publication-v1\x00"))
	_, _ = hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == sha256.Size &&
		hex.EncodeToString(decoded) == value[len("sha256:"):]
}

func equalOverride(left, right *OperatorOverride) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func publicationInvalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPublication, fmt.Sprintf(format, args...))
}
