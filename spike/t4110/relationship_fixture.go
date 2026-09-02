package t4110

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

type emptyObservedSource struct {
	authority observationpublication.DownstreamAuthority
}

func (source emptyObservedSource) DownstreamAuthority() observationpublication.DownstreamAuthority {
	return source.authority
}

func (emptyObservedSource) WalkObserved(
	ctx context.Context,
	_ func(observationpublication.Record, sourceobservation.Observation) error,
) error {
	return ctx.Err()
}

type emptyRelationshipComponents struct {
	resolver *resolvernamespace.Publication
	rpc      *rpccallerposting.Publication
	kafka    *kafkatopicposting.Publication
	upstream downstreamauthority.Authority
}

func publishEmptyRelationshipComponents(
	ctx context.Context,
	dataDir, repository, commit string,
) (emptyRelationshipComponents, error) {
	for _, name := range []string{
		"relationship-resolver-namespaces",
		"relationship-rpc-postings",
		"relationship-kafka-postings",
	} {
		if err := os.MkdirAll(filepath.Join(dataDir, name), 0o700); err != nil {
			return emptyRelationshipComponents{}, fmt.Errorf("create %s root: %w", name, err)
		}
	}
	observation := observationpublication.DownstreamAuthority{
		Version:                observationpublication.DownstreamAuthorityV2,
		Repository:             repository,
		SourceGenerationDigest: fixedDigest('1'), SourceRootDigest: fixedDigest('2'),
		ObservationGenerationDigest: fixedDigest('3'), ObservationRootDigest: fixedDigest('4'),
		PartitionPolicyDigest:   fixedDigest('5'),
		ObservationPolicyDigest: sourceobservation.PolicyDigest(),
		InventoryPolicyDigest:   fixedDigest('6'),
	}
	upstream, err := downstreamauthority.BuildRequired(observation, nil, nil)
	if err != nil {
		return emptyRelationshipComponents{}, err
	}

	resolverStage, err := resolvernamespace.BuildV2(ctx, resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root:       filepath.Join(dataDir, "relationship-resolver-namespaces"),
			Repository: repository, Commit: commit,
			ResolverGenerationDigest: fixedDigest('7'),
			ResolverManifestDigest:   fixedDigest('8'),
		},
		Upstream: upstream,
	})
	if err != nil {
		return emptyRelationshipComponents{}, err
	}
	resolver, err := resolverStage.Publish(ctx)
	if err != nil {
		return emptyRelationshipComponents{}, err
	}

	observed := emptyObservedSource{authority: observation}
	rpcStage, err := rpccallerposting.BuildV2(ctx, rpccallerposting.BuildRequestV2{
		Root:         filepath.Join(dataDir, "relationship-rpc-postings"),
		Observations: observed, Resolver: resolver, Upstream: upstream,
	})
	if err != nil {
		return emptyRelationshipComponents{}, err
	}
	rpc, err := rpcStage.Publish(ctx)
	if err != nil {
		return emptyRelationshipComponents{}, err
	}

	kafkaStage, err := kafkatopicposting.BuildV2(ctx, kafkatopicposting.BuildRequestV2{
		Root:         filepath.Join(dataDir, "relationship-kafka-postings"),
		Observations: observed, Upstream: upstream,
	})
	if err != nil {
		return emptyRelationshipComponents{}, err
	}
	kafka, err := kafkaStage.Publish(ctx)
	if err != nil {
		return emptyRelationshipComponents{}, err
	}
	return emptyRelationshipComponents{
		resolver: resolver,
		rpc:      rpc,
		kafka:    kafka,
		upstream: upstream,
	}, nil
}

func fixedDigest(digit byte) string {
	return "sha256:" + strings.Repeat(string(digit), 64)
}
