package main

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/t421sourceprojection"
)

type t421FinalSourceKey struct {
	repository       string
	searchGeneration string
	sourceGeneration string
	commit           string
	policyDigest     string
}

type t421FinalSourceSnapshot struct {
	Commit     string
	Projection t421sourceprojection.Projection
}

type t421FinalSourceCache struct {
	mu         sync.Mutex
	valid      bool
	key        t421FinalSourceKey
	projection t421sourceprojection.Projection
}

type t421FinalSourcePending struct {
	cache      *t421FinalSourceCache
	key        t421FinalSourceKey
	projection t421sourceprojection.Projection
}

func (cache *t421FinalSourceCache) prepare(
	ctx context.Context,
	repository string,
	controls focusedindex.SearchGenerationControls,
	policies []candidate.Policy,
	policyDigest string,
) (t421FinalSourceSnapshot, *t421FinalSourcePending, error) {
	commit, err := t421FinalHeadCommit(controls)
	if cache == nil || ctx == nil || repository == "" || len(policies) == 0 ||
		policyDigest == "" || err != nil {
		return t421FinalSourceSnapshot{}, nil, errors.New("invalid T42.1 final source authority")
	}
	key := t421FinalSourceKey{
		repository: repository, searchGeneration: controls.Search.Digest,
		sourceGeneration: controls.Source.Digest, commit: commit,
		policyDigest: policyDigest,
	}
	cache.mu.Lock()
	if cache.valid && cache.key == key {
		projection := cloneT421FinalSourceProjection(cache.projection)
		cache.mu.Unlock()
		return t421FinalSourceSnapshot{Commit: commit, Projection: projection}, nil, nil
	}
	cache.mu.Unlock()

	accumulator, err := t421sourceprojection.New(ctx, policies, true)
	if err != nil {
		return t421FinalSourceSnapshot{}, nil, err
	}
	manifest, err := repositoryindex.WalkPublishedSource(
		ctx, controls.Directory, repository, accumulator.Add,
	)
	if err != nil || manifest.Digest != controls.Source.Digest {
		return t421FinalSourceSnapshot{}, nil, errors.Join(
			err, errors.New("T42.1 final source generation changed"),
		)
	}
	projection, err := accumulator.Finish()
	if err != nil || projection.TreeOID == "" {
		return t421FinalSourceSnapshot{}, nil, errors.Join(
			err, errors.New("T42.1 final source projection is incomplete"),
		)
	}
	pending := &t421FinalSourcePending{
		cache: cache, key: key, projection: cloneT421FinalSourceProjection(projection),
	}
	return t421FinalSourceSnapshot{
		Commit: commit, Projection: cloneT421FinalSourceProjection(projection),
	}, pending, nil
}

func t421FinalHeadCommit(controls focusedindex.SearchGenerationControls) (string, error) {
	if len(controls.Source.Revisions) != 1 || len(controls.Search.Revisions) != 1 ||
		len(controls.Receipt.Revisions) != 1 {
		return "", errors.New("T42.1 exact source must be HEAD-only")
	}
	source := controls.Source.Revisions[0]
	search := controls.Search.Revisions[0]
	receipt := controls.Receipt.Revisions[0]
	if source.Selector != "HEAD" || search.Selector != "HEAD" || receipt.Selector != "HEAD" ||
		source.Commit == "" || source != search || source != receipt {
		return "", errors.New("T42.1 exact source revision is inconsistent")
	}
	return source.Commit, nil
}

func cloneT421FinalSourceProjection(
	value t421sourceprojection.Projection,
) t421sourceprojection.Projection {
	value.CandidateInventories = slices.Clone(value.CandidateInventories)
	return value
}
