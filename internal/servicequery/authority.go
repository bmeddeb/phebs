package servicequery

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

var serviceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Authority is the closed query/cursor identity. Current catalog and state
// fences invalidate continuations after lifecycle changes; active identities
// and T34.1 roots prevent an older reader from relabeling results.
type Authority struct {
	Schema                     string `json:"schema"`
	PredicatePolicy            string `json:"predicate_policy"`
	TopologyPolicy             string `json:"topology_policy"`
	Repository                 string `json:"repository"`
	ServiceKey                 string `json:"service_key"`
	Status                     string `json:"status"`
	Incarnation                uint64 `json:"incarnation"`
	RevisionSelector           string `json:"revision_selector"`
	RevisionBranch             string `json:"revision_branch"`
	RevisionCommit             string `json:"revision_commit"`
	ExpressionDigest           string `json:"expression_digest"`
	CurrentCatalogGeneration   string `json:"current_catalog_generation"`
	CatalogControlRevision     uint64 `json:"catalog_control_revision"`
	ActiveCatalogGeneration    string `json:"active_catalog_generation"`
	ActiveSourceGeneration     string `json:"active_source_generation"`
	ActiveDesiredGeneration    string `json:"active_desired_generation"`
	ServiceStateDigest         string `json:"service_state_digest"`
	ServiceStateRevision       uint64 `json:"service_state_revision"`
	StateSummaryDigest         string `json:"state_summary_digest"`
	StateSummaryRevision       uint64 `json:"state_summary_revision"`
	RepositorySourceGeneration string `json:"repository_source_generation"`
	RepositorySearchGeneration string `json:"repository_search_generation"`
	PathDigest                 string `json:"path_digest"`
	PathCount                  int    `json:"path_count"`
	PathBytes                  int    `json:"path_bytes"`
	PredicateAtoms             int    `json:"predicate_atoms"`
	PredicateBytes             int    `json:"predicate_bytes"`
	Digest                     string `json:"digest"`
}

func newAuthority(
	expression string,
	resolved resolvedScope,
	predicateBytes int,
) (Authority, error) {
	pathsDigest, err := pathDigest(resolved.paths)
	if err != nil {
		return Authority{}, invalidWrap("path identity", err)
	}
	authority := Authority{
		Schema: AuthoritySchema, PredicatePolicy: PredicatePolicy,
		TopologyPolicy: resolved.search.TopologyPolicy,
		Repository:     resolved.repository, ServiceKey: resolved.serviceKey,
		Status: resolved.status, Incarnation: resolved.incarnation,
		RevisionSelector:           resolved.revision.Selector,
		RevisionBranch:             resolved.revision.Branch,
		RevisionCommit:             resolved.revision.Commit,
		ExpressionDigest:           expressionDigest(expression),
		CurrentCatalogGeneration:   resolved.currentCatalogGeneration,
		CatalogControlRevision:     resolved.catalogControlRevision,
		ActiveCatalogGeneration:    resolved.activeProjection.CatalogGeneration,
		ActiveSourceGeneration:     resolved.activeProjection.SourceGeneration,
		ActiveDesiredGeneration:    resolved.state.ActiveDesiredGeneration,
		ServiceStateDigest:         resolved.state.StateDigest,
		ServiceStateRevision:       resolved.state.ControlRevision,
		StateSummaryDigest:         resolved.summary.SummaryDigest,
		StateSummaryRevision:       resolved.summary.ControlRevision,
		RepositorySourceGeneration: resolved.search.SourceGenerationDigest,
		RepositorySearchGeneration: resolved.search.Digest,
		PathDigest:                 pathsDigest, PathCount: len(resolved.paths),
		PathBytes: resolved.pathBytes, PredicateAtoms: len(resolved.paths),
		PredicateBytes: predicateBytes,
	}
	digest, err := authorityDigest(authority)
	if err != nil {
		return Authority{}, err
	}
	authority.Digest = digest
	if err := ValidateAuthority(authority); err != nil {
		return Authority{}, err
	}
	return authority, nil
}

// Canonical returns the deterministic closed JSON used by a future transport
// cursor. It validates all bounds and the digest before returning bytes.
func (authority Authority) Canonical() ([]byte, error) {
	if err := ValidateAuthority(authority); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(authority)
	if err != nil {
		return nil, invalidWrap("encode authority", err)
	}
	if len(encoded) > MaxAuthorityBytes {
		return nil, invalidf("query authority exceeds %d bytes", MaxAuthorityBytes)
	}
	return encoded, nil
}

// DecodeAuthority accepts only the exact canonical closed form. Requiring
// byte equality after a strict decode rejects duplicate fields, alternate
// ordering/whitespace, unknown fields, and trailing values at one boundary.
func DecodeAuthority(encoded []byte) (Authority, error) {
	if len(encoded) == 0 || len(encoded) > MaxAuthorityBytes {
		return Authority{}, invalidf(
			"encoded query authority must be between 1 and %d bytes",
			MaxAuthorityBytes,
		)
	}
	var authority Authority
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil {
		return Authority{}, invalidWrap("decode query authority", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Authority{}, invalidf("query authority contains trailing JSON")
	}
	canonical, err := authority.Canonical()
	if err != nil {
		return Authority{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return Authority{}, invalidf("query authority is not canonical")
	}
	return authority, nil
}

// ValidateAuthority verifies a decoded query/cursor identity without needing
// the original query object.
func ValidateAuthority(authority Authority) error {
	if authority.Schema != AuthoritySchema ||
		authority.PredicatePolicy != PredicatePolicy ||
		authority.TopologyPolicy != repositoryindex.DirectTopologyPolicy {
		return invalidf("query authority schema or policy is invalid")
	}
	if err := reponame.Validate(authority.Repository); err != nil {
		return invalidf("query authority repository is not canonical")
	}
	if len(authority.ServiceKey) == 0 ||
		len(authority.ServiceKey) > servicecatalog.MaxServiceKeyBytes ||
		!utf8.ValidString(authority.ServiceKey) ||
		!serviceKeyPattern.MatchString(authority.ServiceKey) {
		return invalidf("query authority service key is invalid")
	}
	if authority.Status != servicecatalog.StatusCurrent &&
		authority.Status != servicecatalog.StatusStale {
		return invalidf("query authority status is not searchable")
	}
	if authority.Incarnation == 0 || !validText(authority.RevisionSelector) ||
		!validText(authority.RevisionBranch) ||
		!gitobj.IsObjectID(authority.RevisionCommit) {
		return invalidf("query authority revision identity is invalid")
	}
	for _, value := range []string{
		authority.ExpressionDigest,
		authority.CurrentCatalogGeneration,
		authority.ActiveCatalogGeneration,
		authority.ActiveSourceGeneration,
		authority.ActiveDesiredGeneration,
		authority.ServiceStateDigest,
		authority.StateSummaryDigest,
		authority.RepositorySourceGeneration,
		authority.RepositorySearchGeneration,
		authority.PathDigest,
		authority.Digest,
	} {
		if !validDigest(value) {
			return invalidf("query authority digest is invalid")
		}
	}
	if authority.CatalogControlRevision == 0 ||
		authority.ServiceStateRevision == 0 ||
		authority.StateSummaryRevision == 0 ||
		authority.PathCount < 1 ||
		authority.PathCount > servicecatalog.MaxServicePaths ||
		authority.PathBytes < 1 ||
		authority.PathBytes > servicecatalog.MaxServicePathBytes ||
		authority.PredicateAtoms != authority.PathCount ||
		authority.PredicateBytes < authority.PathCount*len("^(?:)(?:/|$)") ||
		authority.PredicateBytes > MaxPredicateBytes {
		return invalidf("query authority count, byte, or revision bound is invalid")
	}
	want, err := authorityDigest(authority)
	if err != nil {
		return err
	}
	if authority.Digest != want {
		return invalidf("query authority digest is inconsistent")
	}
	return nil
}

func authorityDigest(authority Authority) (string, error) {
	authority.Digest = ""
	encoded, err := json.Marshal(authority)
	if err != nil {
		return "", fmt.Errorf("%w: encode authority digest: %v", ErrInvalidScope, err)
	}
	return digest("phebs-service-query-authority-v1\x00", encoded), nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == 32
}
