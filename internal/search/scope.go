package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ScopeReceiptSchema = "phebs-search-scope-v1"
	ScopeAllCode       = "all_code"
	ScopeService       = "service"

	allCodeMembershipPolicy = "visible-indexed-repositories-v1"
	serviceMembershipPolicy = "accepted-roles-union-shared-included-unowned-excluded-v1"
)

var (
	ErrInvalidScopeSelector = errors.New("invalid search scope selector")
	ErrScopeNotFound        = errors.New("search scope not found")
)

type ScopeSelector struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository,omitempty"`
	ServiceKey string `json:"service_key,omitempty"`
}

type ScopeRevision struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

// ScopeReceipt is the shared HTTP/MCP/UI identity for one completed search.
// ResultSetDigest binds the exact emitted citations without claiming that a
// truncated or zero-result query exhausts the searched corpus.
type ScopeReceipt struct {
	Schema           string                  `json:"schema"`
	Kind             string                  `json:"kind"`
	Repository       string                  `json:"repository,omitempty"`
	ServiceKey       string                  `json:"service_key,omitempty"`
	ServiceStatus    string                  `json:"service_status,omitempty"`
	MembershipPolicy string                  `json:"membership_policy"`
	ExpressionDigest string                  `json:"expression_digest"`
	Authority        *servicequery.Authority `json:"service_authority,omitempty"`
	Revisions        []ScopeRevision         `json:"revisions"`
	ResultSetDigest  string                  `json:"result_set_digest"`
	ResultFiles      int                     `json:"result_files"`
	ResultMatches    int                     `json:"result_matches"`
	Digest           string                  `json:"digest"`
}

// ScopedSearcher is the complete shared REST/SSE/MCP search boundary. The
// ordinary *Searcher implements it directly. T41.7 also supplies one explicit
// preconstructed v3 adapter without adding configuration or runtime selection.
type ScopedSearcher interface {
	SearchScoped(context.Context, ScopeSelector, string, Options) (*Result, error)
	StreamScoped(
		context.Context,
		ScopeSelector,
		string,
		Options,
		func(*Result),
	) (*Stats, *ScopeReceipt, error)
}

var _ ScopedSearcher = (*Searcher)(nil)

type v3ScopedSearcher struct {
	searcher *Searcher
	reader   *store.ServiceStateV3Reader
}

type runtimeScopedSearcher struct {
	searcher *Searcher
	reader   *store.ServiceStateV3Reader
}

// NewV3ScopedSearcher binds the fixed-backend v3 point/page reader used by
// tests and embeddings. Production constructs NewRuntimeScopedSearcher.
func NewV3ScopedSearcher(
	searcher *Searcher,
	reader *store.ServiceStateV3Reader,
) (ScopedSearcher, error) {
	if searcher == nil || reader == nil {
		return nil, errors.New("v3 scoped search requires a searcher and state reader")
	}
	return &v3ScopedSearcher{searcher: searcher, reader: reader}, nil
}

func (scoped *v3ScopedSearcher) SearchScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
) (*Result, error) {
	return scoped.searcher.SearchScopedV3(ctx, scoped.reader, selector, expression, opts)
}

func (scoped *v3ScopedSearcher) StreamScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
	sink func(*Result),
) (*Stats, *ScopeReceipt, error) {
	return scoped.searcher.StreamScopedV3(
		ctx, scoped.reader, selector, expression, opts, sink,
	)
}

var _ ScopedSearcher = (*v3ScopedSearcher)(nil)

// NewRuntimeScopedSearcher constructs the production selector-aware search
// boundary. All-code queries remain selector-free; service queries choose and
// final-fence one repository-local runtime through SearchServiceSelected.
func NewRuntimeScopedSearcher(
	searcher *Searcher,
	reader *store.ServiceStateV3Reader,
) (ScopedSearcher, error) {
	if searcher == nil || reader == nil || searcher.st == nil {
		return nil, errors.New("runtime scoped search requires a searcher and state reader")
	}
	if _, ok := searcher.st.(servicequery.RuntimeSelectorStore); !ok {
		return nil, errors.New("runtime scoped search requires a selector store")
	}
	return &runtimeScopedSearcher{searcher: searcher, reader: reader}, nil
}

func (scoped *runtimeScopedSearcher) SearchScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
) (*Result, error) {
	return scoped.searcher.searchScoped(
		ctx, selector, expression, opts,
		func(
			ctx context.Context, request ServiceRequest, opts Options,
		) (*ServiceResult, error) {
			return scoped.searcher.SearchServiceSelected(ctx, scoped.reader, request, opts)
		},
	)
}

func (scoped *runtimeScopedSearcher) StreamScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
	sink func(*Result),
) (*Stats, *ScopeReceipt, error) {
	return scoped.searcher.streamScoped(
		ctx, selector, expression, opts, sink, scoped.SearchScoped,
	)
}

var _ ScopedSearcher = (*runtimeScopedSearcher)(nil)

// SearchScoped selects the ordinary All code reader or the exact v2 service
// reader and attaches one closed receipt to the existing result wire shape.
func (s *Searcher) SearchScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
) (*Result, error) {
	return s.searchScoped(ctx, selector, expression, opts, s.SearchService)
}

// SearchScopedV3 is the explicit segmented-service backend used behind the
// runtime selector. It preserves the existing selector, authority, and receipt
// schemas.
func (s *Searcher) SearchScopedV3(
	ctx context.Context,
	reader *store.ServiceStateV3Reader,
	selector ScopeSelector,
	expression string,
	opts Options,
) (*Result, error) {
	return s.searchScoped(
		ctx, selector, expression, opts,
		func(
			ctx context.Context, request ServiceRequest, opts Options,
		) (*ServiceResult, error) {
			return s.SearchServiceV3(ctx, reader, request, opts)
		},
	)
}

type scopedServiceSearch func(
	context.Context,
	ServiceRequest,
	Options,
) (*ServiceResult, error)

func (s *Searcher) searchScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
	searchService scopedServiceSearch,
) (*Result, error) {
	selector, err := validateScopeSelector(selector)
	if err != nil {
		return nil, err
	}
	var result *Result
	var authority *servicequery.Authority
	switch selector.Kind {
	case ScopeAllCode:
		result, err = s.Search(ctx, expression, opts)
	case ScopeService:
		var serviceResult *ServiceResult
		serviceResult, err = searchService(ctx, ServiceRequest{
			Repository: selector.Repository, ServiceKey: selector.ServiceKey,
			Expression: expression, RevisionSelector: "HEAD",
		}, opts)
		if serviceResult != nil {
			result = serviceResult.Result
			copy := serviceResult.Authority
			authority = &copy
		}
	}
	if err != nil {
		if selector.Kind == ScopeService && errors.Is(err, store.ErrNotFound) {
			err = fmt.Errorf("%w: %w", ErrScopeNotFound, err)
		}
		return nil, err
	}
	receipt, err := newScopeReceipt(selector, expression, result, authority)
	if err != nil {
		return nil, err
	}
	result.Scope = &receipt
	return result, nil
}

// StreamScoped preserves progressive All code delivery. Service scope already
// uses one exact static reader, so it emits one bounded result batch.
func (s *Searcher) StreamScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
	sink func(*Result),
) (*Stats, *ScopeReceipt, error) {
	return s.streamScoped(ctx, selector, expression, opts, sink, s.SearchScoped)
}

// StreamScopedV3 is the SSE-equivalent of SearchScopedV3. Service scope remains
// one exact bounded batch; All code retains ordinary progressive streaming.
func (s *Searcher) StreamScopedV3(
	ctx context.Context,
	reader *store.ServiceStateV3Reader,
	selector ScopeSelector,
	expression string,
	opts Options,
	sink func(*Result),
) (*Stats, *ScopeReceipt, error) {
	return s.streamScoped(
		ctx, selector, expression, opts, sink,
		func(
			ctx context.Context,
			selector ScopeSelector,
			expression string,
			opts Options,
		) (*Result, error) {
			return s.SearchScopedV3(ctx, reader, selector, expression, opts)
		},
	)
}

type scopedSearch func(
	context.Context,
	ScopeSelector,
	string,
	Options,
) (*Result, error)

func (s *Searcher) streamScoped(
	ctx context.Context,
	selector ScopeSelector,
	expression string,
	opts Options,
	sink func(*Result),
	searchScoped scopedSearch,
) (*Stats, *ScopeReceipt, error) {
	selector, err := validateScopeSelector(selector)
	if err != nil {
		return nil, nil, err
	}
	if selector.Kind == ScopeService {
		result, err := searchScoped(ctx, selector, expression, opts)
		if err != nil {
			return nil, nil, err
		}
		if len(result.Files) > 0 {
			sink(result)
		}
		stats := result.Stats
		return &stats, result.Scope, nil
	}
	files := make([]FileResult, 0, clamp(opts.MaxMatches, 50, 500))
	stats, err := s.Stream(ctx, expression, opts, func(batch *Result) {
		for _, file := range batch.Files {
			files = append(files, FileResult{
				Repo: file.Repo, Path: file.Path, Ref: file.Ref,
			})
		}
		sink(batch)
	})
	if err != nil {
		return nil, nil, err
	}
	receipt, err := newScopeReceipt(
		selector, expression, &Result{Files: files, Stats: *stats}, nil,
	)
	if err != nil {
		return nil, nil, err
	}
	return stats, &receipt, nil
}

func validateScopeSelector(selector ScopeSelector) (ScopeSelector, error) {
	if selector.Kind == "" {
		selector.Kind = ScopeAllCode
	}
	switch selector.Kind {
	case ScopeAllCode:
		if selector.Repository != "" || selector.ServiceKey != "" {
			return ScopeSelector{}, fmt.Errorf(
				"%w: all_code scope cannot name a service", ErrInvalidScopeSelector,
			)
		}
	case ScopeService:
		if selector.Repository == "" || selector.ServiceKey == "" {
			return ScopeSelector{}, fmt.Errorf(
				"%w: service scope requires repository and service_key", ErrInvalidScopeSelector,
			)
		}
	default:
		return ScopeSelector{}, fmt.Errorf(
			"%w: unsupported search scope %q", ErrInvalidScopeSelector, selector.Kind,
		)
	}
	return selector, nil
}

type scopeCitation struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
}

func newScopeReceipt(
	selector ScopeSelector,
	expression string,
	result *Result,
	authority *servicequery.Authority,
) (ScopeReceipt, error) {
	if result == nil {
		return ScopeReceipt{}, errors.New("search scope result is absent")
	}
	citations := make([]scopeCitation, 0, len(result.Files))
	revisions := make([]ScopeRevision, 0, len(result.Files))
	for _, file := range result.Files {
		citations = append(citations, scopeCitation{
			Repository: file.Repo, Commit: file.Ref, Path: file.Path,
		})
		revisions = append(revisions, ScopeRevision{
			Repository: file.Repo, Commit: file.Ref,
		})
	}
	sort.Slice(citations, func(i, j int) bool {
		if citations[i].Repository != citations[j].Repository {
			return citations[i].Repository < citations[j].Repository
		}
		if citations[i].Commit != citations[j].Commit {
			return citations[i].Commit < citations[j].Commit
		}
		return citations[i].Path < citations[j].Path
	})
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].Repository != revisions[j].Repository {
			return revisions[i].Repository < revisions[j].Repository
		}
		return revisions[i].Commit < revisions[j].Commit
	})
	revisions = slices.Compact(revisions)
	receipt := ScopeReceipt{
		Schema: ScopeReceiptSchema, Kind: selector.Kind,
		Repository: selector.Repository, ServiceKey: selector.ServiceKey,
		ExpressionDigest: scopeDigest("phebs-search-expression-v1\x00", expression),
		Revisions:        revisions, ResultSetDigest: scopeJSONDigest(
			"phebs-search-result-citations-v1\x00", citations,
		),
		ResultFiles: len(result.Files), ResultMatches: result.Stats.MatchCount,
	}
	if selector.Kind == ScopeService {
		if authority == nil {
			return ScopeReceipt{}, errors.New("service search authority is absent")
		}
		receipt.MembershipPolicy = serviceMembershipPolicy
		receipt.ServiceStatus = authority.Status
		copy := *authority
		receipt.Authority = &copy
	} else {
		receipt.MembershipPolicy = allCodeMembershipPolicy
	}
	receipt.Digest = scopeReceiptDigest(receipt)
	return receipt, nil
}

func scopeReceiptDigest(receipt ScopeReceipt) string {
	receipt.Digest = ""
	return scopeJSONDigest("phebs-search-scope-v1\x00", receipt)
}

func scopeJSONDigest(domain string, value any) string {
	raw, _ := json.Marshal(value)
	return scopeDigest(domain, string(raw))
}

func scopeDigest(domain, value string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte(value))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
