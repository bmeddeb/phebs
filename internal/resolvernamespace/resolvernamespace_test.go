package resolvernamespace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
)

func TestCatalogDeterministicSparseLookupAndClosedStates(t *testing.T) {
	root := t.TempDir()
	descriptors := []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/payments/v1", "PaymentsClient", "Charge", "payments.v1.Payments/Charge", "a"),
		descriptor("thrift", "example.test/ledger", "LedgerClient", "Post", "Ledger.Post", "b"),
	}
	descriptors[1].State = "unsupported"
	descriptors[1].Reason = "generated_source_unparseable"
	descriptors[1].DeclarationPath = ""
	descriptors[1].DeclarationLineage = ""

	first := buildPublication(t, root, "sha256:"+strings.Repeat("1", 64), descriptors, nil)
	secondPrepared := buildPrepared(t, root, "sha256:"+strings.Repeat("1", 64), slices.Clone(descriptors), first)
	second, err := secondPrepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Root().Digest != second.Root().Digest {
		t.Fatalf("deterministic root changed: %s != %s", first.Root().Digest, second.Root().Digest)
	}
	_, resolved, err := LookupCurrent(
		t.Context(), root, "github.com/acme/repo", LanguageGo, "grpc",
		"example.test/payments/v1", "PaymentsClient", "Charge",
	)
	if err != nil || len(resolved) != 1 || resolved[0].State != "resolved" ||
		resolved[0].Operation != "payments.v1.Payments/Charge" {
		t.Fatalf("resolved lookup = %+v, %v", resolved, err)
	}
	_, unsupported, err := LookupCurrent(
		t.Context(), root, "github.com/acme/repo", LanguageGo, "thrift",
		"example.test/ledger", "LedgerClient", "Post",
	)
	if err != nil || len(unsupported) != 1 || unsupported[0].State != "unsupported" ||
		unsupported[0].Reason != "generated_source_unparseable" {
		t.Fatalf("unsupported lookup = %+v, %v", unsupported, err)
	}
	_, missing, err := LookupCurrent(
		t.Context(), root, "github.com/acme/repo", LanguageGo, "grpc",
		"example.test/payments/v1", "PaymentsClient", "Missing",
	)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing lookup = %+v, %v", missing, err)
	}
	rootRaw, err := os.ReadFile(filepath.Join(second.directory, "root.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rootRaw), "service_key") || strings.Contains(string(rootRaw), "analysis_unit") {
		t.Fatalf("catalog authority acquired ownership vocabulary: %s", rootRaw)
	}
}

func TestOnlyAffectedNamespaceRebuilds(t *testing.T) {
	root := t.TempDir()
	firstDescriptors := []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/Read", "a"),
		descriptor("grpc", "example.test/b", "BClient", "Read", "b.Service/Read", "b"),
	}
	first := buildPublication(t, root, "sha256:"+strings.Repeat("1", 64), firstDescriptors, nil)
	secondDescriptors := slices.Clone(firstDescriptors)
	secondDescriptors[1] = descriptor(
		"grpc", "example.test/b", "BClient", "Read", "b.Service/ReadV2", "c",
	)
	second := buildPublication(t, root, "sha256:"+strings.Repeat("2", 64), secondDescriptors, first)

	firstA := receipt(t, first, "grpc", "example.test/a")
	secondA := receipt(t, second, "grpc", "example.test/a")
	firstB := receipt(t, first, "grpc", "example.test/b")
	secondB := receipt(t, second, "grpc", "example.test/b")
	if firstA.ContentDigest != secondA.ContentDigest || firstB.ContentDigest == secondB.ContentDigest {
		t.Fatalf("unexpected namespace digests: A=%s/%s B=%s/%s",
			firstA.ContentDigest, secondA.ContentDigest,
			firstB.ContentDigest, secondB.ContentDigest)
	}
	firstInfo, err := os.Stat(filepath.Join(first.directory, firstA.Member))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(filepath.Join(second.directory, secondA.Member))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("unchanged namespace was rewritten instead of hard-linked")
	}
	firstChanged, _ := os.Stat(filepath.Join(first.directory, firstB.Member))
	secondChanged, _ := os.Stat(filepath.Join(second.directory, secondB.Member))
	if os.SameFile(firstChanged, secondChanged) {
		t.Fatal("changed namespace unexpectedly reused prior bytes")
	}
}

func TestConflictAndAmbiguityRemainRecords(t *testing.T) {
	root := t.TempDir()
	first := descriptor("grpc", "example.test/shared", "SharedClient", "Get", "one.Service/Get", "a")
	second := descriptor("grpc", "example.test/shared", "SharedClient", "Get", "two.Service/Get", "b")
	ambiguous := descriptor("grpc", "example.test/shared", "SharedClient", "Put", "shared.Service/Put", "c")
	ambiguous.State = "ambiguous"
	ambiguous.Reason = "multiple_declaration_paths"
	ambiguous.DeclarationPath = ""
	ambiguous.DeclarationLineage = ""
	publication := buildPublication(
		t, root, "sha256:"+strings.Repeat("1", 64),
		[]gocaller.DirectDescriptor{second, ambiguous, first}, nil,
	)
	conflicted, err := publication.Lookup(
		t.Context(), LanguageGo, "grpc", "example.test/shared", "SharedClient", "Get",
	)
	if err != nil || len(conflicted) != 3 {
		t.Fatalf("conflicted lookup = %+v, %v", conflicted, err)
	}
	found := false
	for _, record := range conflicted {
		if record.State == "conflict" {
			found = len(record.Candidates) == 2 &&
				record.Candidates[0].Operation == "one.Service/Get" &&
				record.Candidates[1].Operation == "two.Service/Get"
		}
	}
	if !found {
		t.Fatalf("explicit conflict record missing: %+v", conflicted)
	}
	abstention, err := publication.Lookup(
		t.Context(), LanguageGo, "grpc", "example.test/shared", "SharedClient", "Put",
	)
	if err != nil || len(abstention) != 1 || abstention[0].State != "ambiguous" {
		t.Fatalf("ambiguous lookup = %+v, %v", abstention, err)
	}
}

func TestDuplicateGeneratedOccurrencesForOneTargetAreNotConflicts(t *testing.T) {
	root := t.TempDir()
	first := descriptor("grpc", "example.test/shared", "SharedClient", "Get", "one.Service/Get", "a")
	second := descriptor("grpc", "example.test/shared", "SharedClient", "Get", "one.Service/Get", "b")
	second.DeclarationPath = first.DeclarationPath
	second.DeclarationLineage = first.DeclarationLineage
	publication := buildPublication(
		t, root, "sha256:"+strings.Repeat("1", 64),
		[]gocaller.DirectDescriptor{first, second}, nil,
	)
	values, err := publication.Lookup(
		t.Context(), LanguageGo, "grpc", "example.test/shared", "SharedClient", "Get",
	)
	if err != nil || len(values) != 2 {
		t.Fatalf("duplicate generated occurrences = %+v, %v", values, err)
	}
	for _, value := range values {
		if value.State == "conflict" {
			t.Fatalf("equal targets became a conflict: %+v", values)
		}
	}
}

func TestRecoveryValidatesCompleteGenerationBeforePointerSwap(t *testing.T) {
	root := t.TempDir()
	old := buildPublication(t, root, "sha256:"+strings.Repeat("1", 64), []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/Read", "a"),
	}, nil)
	prepared := buildPrepared(t, root, "sha256:"+strings.Repeat("2", 64), []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/ReadV2", "b"),
		descriptor("thrift", "example.test/b", "BClient", "Read", "B.Read", "c"),
	}, old)
	installed, err := prepared.install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(installed.base, installed.pointer); err != nil {
		t.Fatal(err)
	}
	current, err := OpenCurrent(t.Context(), root, "github.com/acme/repo")
	if err != nil || current.Root().Digest != old.Root().Digest {
		t.Fatalf("marker changed authority before recovery: %v", err)
	}
	recovered, err := Recover(t.Context(), root, "github.com/acme/repo")
	if err != nil || recovered.Root().Digest != installed.Root().Digest {
		t.Fatalf("recover = %+v, %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(installed.base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publishing marker remains: %v", err)
	}

	brokenPrepared := buildPrepared(t, root, "sha256:"+strings.Repeat("3", 64), []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/ReadV3", "d"),
		descriptor("thrift", "example.test/b", "BClient", "Read", "B.Read", "c"),
	}, recovered)
	broken, err := brokenPrepared.install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(broken.base, broken.pointer); err != nil {
		t.Fatal(err)
	}
	brokenReceipt := receipt(t, broken, "grpc", "example.test/a")
	if err := os.WriteFile(filepath.Join(broken.directory, brokenReceipt.Member), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(t.Context(), root, "github.com/acme/repo"); err == nil {
		t.Fatal("corrupt marked generation recovered")
	}
	stillCurrent, err := OpenCurrent(t.Context(), root, "github.com/acme/repo")
	if err != nil || stillCurrent.Root().Digest != recovered.Root().Digest {
		t.Fatalf("corrupt recovery displaced prior current: %v", err)
	}
}

func TestSparseLookupDoesNotReadSiblingNamespace(t *testing.T) {
	root := t.TempDir()
	publication := buildPublication(t, root, "sha256:"+strings.Repeat("1", 64), []gocaller.DirectDescriptor{
		descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/Read", "a"),
		descriptor("grpc", "example.test/b", "BClient", "Read", "b.Service/Read", "b"),
	}, nil)
	broken := receipt(t, publication, "grpc", "example.test/b")
	if err := os.WriteFile(filepath.Join(publication.directory, broken.Member), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := publication.Lookup(
		t.Context(), LanguageGo, "grpc", "example.test/a", "AClient", "Read",
	)
	if err != nil || len(result) != 1 {
		t.Fatalf("sparse sibling lookup = %+v, %v", result, err)
	}
	if _, err := publication.Lookup(
		t.Context(), LanguageGo, "grpc", "example.test/b", "BClient", "Read",
	); err == nil {
		t.Fatal("corrupt selected namespace was accepted")
	}
}

func TestConflictCandidateBoundCheckedBeforeGrowth(t *testing.T) {
	descriptors := make([]gocaller.DirectDescriptor, 0, MaxConflictCandidates+1)
	for index := 0; index <= MaxConflictCandidates; index++ {
		descriptors = append(descriptors, descriptor(
			"grpc", "example.test/conflict", "ConflictClient", "Read",
			fmt.Sprintf("service.%02d/Read", index), fmt.Sprintf("seed-%02d", index),
		))
	}
	_, err := Build(t.Context(), BuildRequest{
		Root: t.TempDir(), Repository: "github.com/acme/repo", Commit: strings.Repeat("f", 40),
		ResolverGenerationDigest: "sha256:" + strings.Repeat("1", 64),
		ResolverManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		Descriptors:              descriptors,
	})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("conflict bound error = %v", err)
	}
}

func TestCatalogRefusesSymlinkedNamespaceRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "resolver-namespaces")); err != nil {
		t.Fatal(err)
	}
	_, err := Build(t.Context(), BuildRequest{
		Root: root, Repository: "github.com/acme/repo", Commit: strings.Repeat("f", 40),
		ResolverGenerationDigest: "sha256:" + strings.Repeat("1", 64),
		ResolverManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		Descriptors: []gocaller.DirectDescriptor{
			descriptor("grpc", "example.test/a", "AClient", "Read", "a.Service/Read", "a"),
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlinked namespace root error = %v", err)
	}
}

func buildPrepared(
	t *testing.T,
	root, generation string,
	descriptors []gocaller.DirectDescriptor,
	prior *Publication,
) *Prepared {
	t.Helper()
	prepared, err := Build(t.Context(), BuildRequest{
		Root: root, Repository: "github.com/acme/repo", Commit: strings.Repeat("f", 40),
		ResolverGenerationDigest: generation,
		ResolverManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		Descriptors:              descriptors, Prior: prior,
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func buildPublication(
	t *testing.T,
	root, generation string,
	descriptors []gocaller.DirectDescriptor,
	prior *Publication,
) *Publication {
	t.Helper()
	publication, err := buildPrepared(t, root, generation, descriptors, prior).Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func receipt(t *testing.T, publication *Publication, protocol, namespace string) NamespaceReceipt {
	t.Helper()
	for _, value := range publication.Root().Namespaces {
		if value.Language == LanguageGo && value.Protocol == protocol && value.Namespace == namespace {
			return value
		}
	}
	t.Fatalf("missing receipt %s/%s", protocol, namespace)
	return NamespaceReceipt{}
}

func descriptor(protocol, namespace, client, method, operation, seed string) gocaller.DirectDescriptor {
	digest := sha256.Sum256([]byte(seed))
	hexDigest := hex.EncodeToString(digest[:])
	declarationSuffix := ".proto"
	generatorRelativePath := "api/" + hexDigest[:12] + declarationSuffix
	if protocol == "thrift" {
		declarationSuffix = ".thrift"
		generatorRelativePath = ""
	}
	return gocaller.DirectDescriptor{
		State: "resolved", Protocol: protocol, ImportPath: namespace,
		Package: "fixture", ClientType: client, Method: method, Operation: operation,
		Constructors: []string{"New" + client}, GeneratedPath: "gen/" + hexDigest[:12] + ".go",
		GeneratedObjectID:     hexDigest[:40],
		GeneratedDigest:       "sha256:" + hexDigest,
		GeneratorRelativePath: generatorRelativePath,
		DeclarationPath:       "api/" + hexDigest[:12] + declarationSuffix,
		DeclarationLineage:    "lineage-" + seed,
	}
}
