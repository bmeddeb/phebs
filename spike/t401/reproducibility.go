package t401

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

type ReproducibilityRequest struct {
	Structural [2]string
	Semantic   [2]string
}

type authoredBuild struct {
	profile       Profile
	oracle        Oracle
	manifest      Manifest
	receipt       Receipt
	profileBytes  []byte
	oracleBytes   []byte
	manifestBytes []byte
	receiptBytes  []byte
}

// BuildReproducibilityReceipt compares two separately authored frozen outputs
// per profile. Paths are input authority only and never enter the result.
func BuildReproducibilityReceipt(request ReproducibilityRequest) (ReproducibilityReceipt, error) {
	paths := []string{
		request.Structural[0], request.Structural[1],
		request.Semantic[0], request.Semantic[1],
	}
	seen := make(map[string]struct{}, len(paths))
	for _, input := range paths {
		if input == "" || !filepath.IsAbs(input) {
			return ReproducibilityReceipt{}, errors.New("T40.1 reproducibility input must be absolute")
		}
		resolved, err := filepath.EvalSymlinks(input)
		if err != nil {
			return ReproducibilityReceipt{}, fmt.Errorf("resolve T40.1 authored input: %w", err)
		}
		if _, duplicate := seen[resolved]; duplicate {
			return ReproducibilityReceipt{}, errors.New("T40.1 reproducibility inputs must be separate outputs")
		}
		seen[resolved] = struct{}{}
	}
	structural, err := CompareAuthoredPair(request.Structural[0], request.Structural[1])
	if err != nil {
		return ReproducibilityReceipt{}, err
	}
	semantic, err := CompareAuthoredPair(request.Semantic[0], request.Semantic[1])
	if err != nil {
		return ReproducibilityReceipt{}, err
	}
	receipt := ReproducibilityReceipt{
		Schema:   ReproducibilitySchema,
		Profiles: []ReproducibilityProfile{structural, semantic},
		Claims: ReproducibilityClaims{
			Synthetic: true, SourceFree: true, SeparateAuthorInvocations: true,
		},
	}
	if err := ValidateReproducibilityReceipt(receipt); err != nil {
		return ReproducibilityReceipt{}, err
	}
	return receipt, nil
}

// CompareAuthoredPair validates and compares two complete author outputs. It
// is also used by projection tests; full retained authority is granted only by
// ValidateReproducibilityReceipt over both frozen profiles.
func CompareAuthoredPair(firstPath, secondPath string) (ReproducibilityProfile, error) {
	first, err := readAuthoredBuild(firstPath)
	if err != nil {
		return ReproducibilityProfile{}, err
	}
	second, err := readAuthoredBuild(secondPath)
	if err != nil {
		return ReproducibilityProfile{}, err
	}
	if first.profile.Name != second.profile.Name || first.profile.Kind != second.profile.Kind ||
		first.profile.Scale != second.profile.Scale || first.receipt.ProfileDigest != second.receipt.ProfileDigest {
		return ReproducibilityProfile{}, errors.New("T40.1 authored pair has crossed profile authority")
	}
	profile := ReproducibilityProfile{
		Profile: first.profile.Name, Kind: first.profile.Kind, Scale: first.profile.Scale,
		ProfileDigest: first.receipt.ProfileDigest,
		Builds: []AuthorBuildObservation{
			buildObservation(1, first), buildObservation(2, second),
		},
		ProfileBytesEqual:     bytes.Equal(first.profileBytes, second.profileBytes),
		OracleBytesEqual:      bytes.Equal(first.oracleBytes, second.oracleBytes),
		ManifestBytesEqual:    bytes.Equal(first.manifestBytes, second.manifestBytes),
		RevisionIdentityEqual: slices.Equal(first.manifest.Revisions, second.manifest.Revisions),
	}
	if err := validateReproducibilityProfile(profile, first.profile); err != nil {
		return ReproducibilityProfile{}, err
	}
	return profile, nil
}

func ValidateReproducibilityReceipt(receipt ReproducibilityReceipt) error {
	if receipt.Schema != ReproducibilitySchema || len(receipt.Profiles) != 2 ||
		!receipt.Claims.Synthetic || !receipt.Claims.SourceFree ||
		!receipt.Claims.SeparateAuthorInvocations || receipt.Claims.EstablishesTargetSLO ||
		receipt.Claims.QualifiesSupportedScale || receipt.Claims.AuthorizesRelease {
		return errors.New("T40.1 reproducibility receipt is invalid")
	}
	frozen, err := FrozenProfiles()
	if err != nil {
		return err
	}
	for index, profile := range receipt.Profiles {
		if err := validateReproducibilityProfile(profile, frozen[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateReproducibilityProfile(profile ReproducibilityProfile, authority Profile) error {
	digest, err := ProfileDigest(authority)
	if err != nil {
		return err
	}
	if profile.Profile == "" || (profile.Kind != "structural" && profile.Kind != "semantic") ||
		(profile.Scale != "frozen" && profile.Scale != "projection") || !validDigest(profile.ProfileDigest) ||
		len(profile.Builds) != 2 || !profile.ProfileBytesEqual || !profile.OracleBytesEqual ||
		!profile.ManifestBytesEqual || !profile.RevisionIdentityEqual {
		return errors.New("T40.1 authored pair is not byte- and identity-equal")
	}
	if profile.Profile != authority.Name || profile.Kind != authority.Kind ||
		profile.Scale != authority.Scale || profile.ProfileDigest != digest {
		return errors.New("T40.1 reproducibility profile differs from its authority")
	}
	for index, build := range profile.Builds {
		if build.Ordinal != uint64(index+1) || len(build.Artifacts) != 4 || len(build.Revisions) != 3 ||
			build.Git.Name != "git" || build.Git.Version == "" || build.BareGitLogicalBytes == 0 {
			return errors.New("T40.1 build observation is invalid")
		}
		want, err := reconstructBuildArtifacts(authority, build)
		if err != nil {
			return err
		}
		if !slices.Equal(build.Artifacts, want) {
			return errors.New("T40.1 build artifacts differ from reconstructed authority")
		}
	}
	for artifactIndex := 0; artifactIndex < 3; artifactIndex++ {
		if profile.Builds[0].Artifacts[artifactIndex] != profile.Builds[1].Artifacts[artifactIndex] {
			return errors.New("T40.1 deterministic author artifacts differ")
		}
	}
	if !slices.Equal(profile.Builds[0].Revisions, profile.Builds[1].Revisions) {
		return errors.New("T40.1 deterministic revision identities differ")
	}
	return nil
}

func reconstructBuildArtifacts(profile Profile, build AuthorBuildObservation) ([]ArtifactIdentity, error) {
	oracle, err := DeriveOracle(profile)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		Schema: ManifestSchema, Profile: profile.Name,
		ProfileDigest: oracle.ProfileDigest, Repository: RepositoryName,
		Projection: profile.Scale == "projection", Aggregate: oracle.Aggregate,
		Revisions: slices.Clone(build.Revisions), FailurePoints: failurePointNames(),
		ByteMetrics: []ByteMetric{
			{Kind: "declared_repository_go", Measurement: "exact", Bytes: oracle.Aggregate.DeclaredRepositoryGoBytes},
			{Kind: "logical_source", Measurement: "exact", Bytes: oracle.Aggregate.LogicalSourceBytes},
		},
	}
	if err := ValidateManifest(manifest, profile, oracle); err != nil {
		return nil, err
	}
	profileBytes, err := MarshalCanonical(profile)
	if err != nil {
		return nil, err
	}
	oracleBytes, err := MarshalCanonical(oracle)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := MarshalCanonical(manifest)
	if err != nil {
		return nil, err
	}
	artifacts := []ArtifactIdentity{
		{Name: "profile.json", Bytes: uint64(len(profileBytes)), SHA256: SHA256(profileBytes)},
		{Name: "oracle.json", Bytes: uint64(len(oracleBytes)), SHA256: SHA256(oracleBytes)},
		{Name: "manifest.json", Bytes: uint64(len(manifestBytes)), SHA256: SHA256(manifestBytes)},
	}
	receipt := Receipt{
		Schema: ReceiptSchema, Outcome: "authored", Profile: profile.Name,
		ProfileDigest: oracle.ProfileDigest, Artifacts: slices.Clone(artifacts),
		Revisions: slices.Clone(build.Revisions), Git: build.Git,
		HistoricalDiagnostic: FrozenHistoricalDiagnostic(), Claims: profile.Claims,
		ByteMetrics: []ByteMetric{
			{Kind: "profile_canonical", Measurement: "exact", Bytes: uint64(len(profileBytes))},
			{Kind: "oracle_canonical", Measurement: "exact", Bytes: uint64(len(oracleBytes))},
			{Kind: "manifest_canonical", Measurement: "exact", Bytes: uint64(len(manifestBytes))},
			{Kind: "declared_repository_go", Measurement: "exact", Bytes: oracle.Aggregate.DeclaredRepositoryGoBytes},
			{Kind: "logical_source", Measurement: "exact", Bytes: oracle.Aggregate.LogicalSourceBytes},
			{Kind: "bare_git_logical", Measurement: "exact", Bytes: build.BareGitLogicalBytes},
			{Kind: "bare_git_allocated", Measurement: "exact", Bytes: build.BareGitAllocatedBytes},
		},
	}
	if err := ValidateReceipt(receipt, profile, manifest); err != nil {
		return nil, err
	}
	receiptBytes, err := MarshalCanonical(receipt)
	if err != nil {
		return nil, err
	}
	return append(artifacts, ArtifactIdentity{
		Name: "receipt.json", Bytes: uint64(len(receiptBytes)), SHA256: SHA256(receiptBytes),
	}), nil
}

func readAuthoredBuild(root string) (authoredBuild, error) {
	profileBytes, err := readStableRegular(filepath.Join(root, "profile.json"), 1<<20)
	if err != nil {
		return authoredBuild{}, err
	}
	oracleBytes, err := readStableRegular(filepath.Join(root, "oracle.json"), 1<<20)
	if err != nil {
		return authoredBuild{}, err
	}
	manifestBytes, err := readStableRegular(filepath.Join(root, "manifest.json"), 1<<20)
	if err != nil {
		return authoredBuild{}, err
	}
	receiptBytes, err := readStableRegular(filepath.Join(root, "receipt.json"), 1<<20)
	if err != nil {
		return authoredBuild{}, err
	}
	profile, err := DecodeStrict[Profile](profileBytes)
	if err != nil {
		return authoredBuild{}, err
	}
	if err := ValidateProfile(profile); err != nil {
		return authoredBuild{}, err
	}
	canonicalProfile, err := MarshalCanonical(profile)
	if err != nil || !bytes.Equal(profileBytes, canonicalProfile) {
		return authoredBuild{}, errors.New("T40.1 profile artifact is not canonical")
	}
	oracle, err := DecodeStrict[Oracle](oracleBytes)
	if err != nil {
		return authoredBuild{}, err
	}
	if err := ValidateOracle(oracle, profile); err != nil {
		return authoredBuild{}, err
	}
	canonicalOracle, err := MarshalCanonical(oracle)
	if err != nil || !bytes.Equal(oracleBytes, canonicalOracle) {
		return authoredBuild{}, errors.New("T40.1 oracle artifact is not canonical")
	}
	manifest, err := DecodeStrict[Manifest](manifestBytes)
	if err != nil {
		return authoredBuild{}, err
	}
	if err := ValidateManifest(manifest, profile, oracle); err != nil {
		return authoredBuild{}, err
	}
	canonicalManifest, err := MarshalCanonical(manifest)
	if err != nil || !bytes.Equal(manifestBytes, canonicalManifest) {
		return authoredBuild{}, errors.New("T40.1 manifest artifact is not canonical")
	}
	receipt, err := DecodeStrict[Receipt](receiptBytes)
	if err != nil {
		return authoredBuild{}, err
	}
	if err := ValidateReceipt(receipt, profile, manifest); err != nil {
		return authoredBuild{}, err
	}
	canonicalReceipt, err := MarshalCanonical(receipt)
	if err != nil || !bytes.Equal(receiptBytes, canonicalReceipt) {
		return authoredBuild{}, errors.New("T40.1 author receipt is not canonical")
	}
	return authoredBuild{
		profile: profile, oracle: oracle, manifest: manifest, receipt: receipt,
		profileBytes: profileBytes, oracleBytes: oracleBytes,
		manifestBytes: manifestBytes, receiptBytes: receiptBytes,
	}, nil
}

func buildObservation(ordinal uint64, build authoredBuild) AuthorBuildObservation {
	artifacts := slices.Clone(build.receipt.Artifacts)
	artifacts = append(artifacts, ArtifactIdentity{
		Name: "receipt.json", Bytes: uint64(len(build.receiptBytes)), SHA256: SHA256(build.receiptBytes),
	})
	logical, _ := byteMetric(build.receipt.ByteMetrics, "bare_git_logical")
	allocated, _ := byteMetric(build.receipt.ByteMetrics, "bare_git_allocated")
	return AuthorBuildObservation{
		Ordinal: ordinal, Artifacts: artifacts,
		Revisions: slices.Clone(build.receipt.Revisions), Git: build.receipt.Git,
		BareGitLogicalBytes: logical, BareGitAllocatedBytes: allocated,
	}
}

func byteMetric(metrics []ByteMetric, kind string) (uint64, bool) {
	for _, metric := range metrics {
		if metric.Kind == kind {
			return metric.Bytes, true
		}
	}
	return 0, false
}

func readStableRegular(path string, maxBytes int64) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("T40.1 author artifact path is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || !os.SameFile(pathInfo, before) ||
		before.Size() < 0 || before.Size() > maxBytes {
		return nil, errors.New("T40.1 author artifact is not a bounded regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(content)) != before.Size() {
		return nil, errors.New("T40.1 author artifact read is incomplete")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("T40.1 author artifact changed during read")
	}
	return content, nil
}
