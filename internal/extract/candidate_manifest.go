package extract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/gitobj"
)

const candidateManifestInventoryPrefix = "candidate-manifest-v1-"

// CandidateManifestDomain identifies the extractor and candidate-policy
// generation the publication must contain. Candidate policies remain
// separate from the extractor's Candidate predicate: the publication plans
// enumeration, while Required on each record pins the mandatory-read ledger.
type CandidateManifestDomain struct {
	Domain  string
	Version string
}

// CandidateManifestRequest binds one strict publication open to the exact
// indexed state. The provider owns the current candidate-policy identities
// and must reject a stale policy, commit, unit, partial publication, malformed
// row, duplicate row, or missing member before returning.
type CandidateManifestRequest struct {
	Repository   string
	Commit       string
	AnalysisUnit *analysisunit.State
	Domains      []CandidateManifestDomain
}

// CandidateManifestFile is one repository-view planning record. InUnit is
// carried forward for T30.5, but T30.4 deliberately does not filter on it.
// Mode may be empty when the strict provider has already proven a regular
// blob; otherwise it must be one of Git's two regular-file modes.
type CandidateManifestFile struct {
	Path          string
	ObjectID      string
	Mode          string
	DeclaredBytes int64
	Required      bool
	InUnit        bool
}

// CandidateManifestGitlinks is the complete census's repository-boundary
// record. Samples are optional; count and digest are authoritative.
type CandidateManifestGitlinks struct {
	Count           int
	Digest          string
	SamplePaths     []string
	SampleTruncated bool
}

// CandidateManifest is already fully and strictly validated when Open
// returns. Domain iteration is a bounded replay of canonical repository-view
// rows and must never select on InUnit until T30.5.
type CandidateManifest interface {
	Identity() string
	CorpusFileCount() int
	GitlinkBoundaries() CandidateManifestGitlinks
	ForEachRepositoryFile(
		ctx context.Context,
		domain, version string,
		visit func(CandidateManifestFile) error,
	) error
}

// CandidateManifestProvider opens the current strict publication. Production
// server wiring requires it; nil remains supported for isolated/direct legacy
// tests, whose WalkFiles result is their complete inventory.
type CandidateManifestProvider interface {
	OpenCandidateManifest(
		ctx context.Context,
		request CandidateManifestRequest,
	) (CandidateManifest, error)
}

// CandidateManifestIdentityProvider is the pointer-only half of production
// candidate admission. It must validate the request against the committed
// publication pointer and refuse an active publication marker, but it must
// not open, hash, parse, or spool candidate publication bytes.
//
// Worker uses this optional capability to prove that every extraction domain
// is already current before taking the repository mirror lock. Providers that
// do not implement it retain the strict open-first compatibility path.
type CandidateManifestIdentityProvider interface {
	CandidateManifestIdentity(
		ctx context.Context,
		request CandidateManifestRequest,
	) (string, error)
}

func manifestRequest(
	repoName, commit string,
	unit *analysisunit.State,
	extractors []registeredExtractor,
) CandidateManifestRequest {
	domains := make([]CandidateManifestDomain, 0, len(extractors))
	for _, ex := range extractors {
		domains = append(domains, CandidateManifestDomain{
			Domain: ex.domain, Version: ex.version,
		})
	}
	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Domain != domains[j].Domain {
			return domains[i].Domain < domains[j].Domain
		}
		return domains[i].Version < domains[j].Version
	})
	return CandidateManifestRequest{
		Repository: repoName, Commit: commit,
		AnalysisUnit: analysisunit.CloneState(unit),
		Domains:      domains,
	}
}

func validateCandidateManifest(
	manifest CandidateManifest,
) (string, gitlinkInventory, error) {
	if manifest == nil {
		return "", gitlinkInventory{}, errors.New("candidate manifest provider returned nil")
	}
	identity := manifest.Identity()
	inventoryPolicy, err := candidateManifestInventoryPolicy(identity)
	if err != nil {
		return "", gitlinkInventory{}, err
	}
	count := manifest.CorpusFileCount()
	if count < 0 || count > candidate.MaxCorpusEntries {
		return "", gitlinkInventory{}, fmt.Errorf(
			"candidate manifest corpus count %d is outside [0,%d]",
			count, candidate.MaxCorpusEntries)
	}
	input := manifest.GitlinkBoundaries()
	boundaries := gitlinkInventory{
		count: input.Count, digest: input.Digest,
		samplePaths:     slices.Clone(input.SamplePaths),
		sampleTruncated: input.SampleTruncated,
	}
	if boundaries.count < 0 || boundaries.count > candidate.MaxCorpusEntries ||
		!validSHA256(boundaries.digest) ||
		len(boundaries.samplePaths) > maxGitlinkSamplePaths ||
		len(boundaries.samplePaths) > boundaries.count {
		return "", gitlinkInventory{}, errors.New(
			"candidate manifest gitlink inventory is inconsistent")
	}
	sampleBytes := 0
	for index, filePath := range boundaries.samplePaths {
		if err := checkCorpusPath(filePath); err != nil {
			return "", gitlinkInventory{}, fmt.Errorf(
				"candidate manifest gitlink sample: %w", err)
		}
		if index > 0 && boundaries.samplePaths[index-1] >= filePath {
			return "", gitlinkInventory{}, errors.New(
				"candidate manifest gitlink samples are not sorted and unique")
		}
		sampleBytes += len(filePath)
	}
	if sampleBytes > maxGitlinkSampleBytes ||
		boundaries.sampleTruncated &&
			boundaries.count <= len(boundaries.samplePaths) {
		return "", gitlinkInventory{}, errors.New(
			"candidate manifest gitlink sample exceeds its bounds")
	}
	return inventoryPolicy, boundaries, nil
}

func candidateManifestInventoryPolicy(identity string) (string, error) {
	if !validSHA256(identity) {
		return "", errors.New("candidate manifest has invalid identity")
	}
	return candidateManifestInventoryPrefix +
		strings.TrimPrefix(identity, "sha256:"), nil
}

func normalizeManifestFile(input CandidateManifestFile) (treeRecord, error) {
	if err := checkCorpusPath(input.Path); err != nil {
		return treeRecord{}, err
	}
	if !gitobj.IsObjectID(input.ObjectID) {
		return treeRecord{}, errors.New("candidate manifest record has invalid object id")
	}
	if input.DeclaredBytes < 0 {
		return treeRecord{}, errors.New("candidate manifest record has negative declared size")
	}
	if input.Mode != "" && input.Mode != "100644" && input.Mode != "100755" {
		return treeRecord{}, fmt.Errorf(
			"candidate manifest record has unsupported mode %q", input.Mode)
	}
	return treeRecord{
		mode: input.Mode, objectType: "blob", oid: input.ObjectID,
		path: input.Path, size: input.DeclaredBytes,
	}, nil
}
