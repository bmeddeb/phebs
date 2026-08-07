package t401

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

func TestSemanticGoTemplatesMatchSourceObservation(t *testing.T) {
	profile, err := mapFrozenProfile("semantic")
	if err != nil {
		t.Fatal(err)
	}
	var start uint64
	for _, family := range profile.Families {
		if family.Plane != "go" {
			continue
		}
		familyStart := start
		start += family.Inputs
		for _, endpoint := range []struct {
			name    string
			ordinal uint64
		}{{"first", familyStart}, {"last", start - 1}} {
			t.Run(family.Name+"/"+endpoint.name, func(t *testing.T) {
				content, err := goContent(profile, family.Name, endpoint.ordinal, false)
				if err != nil {
					t.Fatal(err)
				}
				observation, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{
					Path: "neutral.go", Content: string(content),
				})
				if err != nil {
					t.Fatal(err)
				}
				if family.RecordsPerInput != 1 {
					t.Fatalf("records/input = %d", family.RecordsPerInput)
				}
				var reasons []string
				for _, function := range observation.Functions {
					for _, gap := range function.Gaps {
						reasons = append(reasons, gap.Reason)
					}
				}
				for _, site := range observation.TopicSites {
					if site.State != "resolved" {
						reasons = append(reasons, site.Reason)
					}
				}
				if uint64(len(reasons)) != family.GapsPerInput {
					t.Fatalf("gap reasons = %v, want %d", reasons, family.GapsPerInput)
				}
				if family.Outcome == "gap" && (len(reasons) != 1 || reasons[0] != family.Reason) {
					t.Fatalf("gap reasons = %v, want %q", reasons, family.Reason)
				}
				expectation := adapterExpectationFor(t, profile, family.Name)
				switch expectation.Adapter {
				case "caller":
					result, err := sourceobservation.AdaptCallers(observation, expectation.Protocol, []sourceobservation.MethodDescriptor{{
						Protocol: expectation.Protocol, ImportPath: expectation.ImportPath,
						ClientType: expectation.ClientType, Method: expectation.Method, Operation: expectation.Operation,
					}})
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Evidence) != 1 || result.Evidence[0].State != expectation.State ||
						result.Evidence[0].Reason != expectation.Reason ||
						expectation.State == "resolved" && result.Evidence[0].Operation != expectation.Operation {
						t.Fatalf("caller adapter = %+v, want %+v", result, expectation)
					}
				case "kafka":
					result, err := sourceobservation.AdaptKafka(observation, expectation.Plane)
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Evidence) != 1 || result.Evidence[0].State != expectation.State ||
						result.Evidence[0].Reason != expectation.Reason {
						t.Fatalf("Kafka adapter = %+v, want %+v", result, expectation)
					}
				default:
					t.Fatalf("unknown adapter %q", expectation.Adapter)
				}
				canonical, err := sourceobservation.Canonical(observation)
				if err != nil {
					t.Fatal(err)
				}
				if uint64(len(canonical)) != family.CanonicalBytesPerInput {
					t.Fatalf("canonical bytes = %d, want %d", len(canonical), family.CanonicalBytesPerInput)
				}
			})
		}
	}
}

func adapterExpectationFor(t *testing.T, profile Profile, family string) AdapterExpectation {
	t.Helper()
	for _, expectation := range profile.Adapters {
		if expectation.Family == family {
			return expectation
		}
	}
	t.Fatalf("adapter expectation for %q is absent", family)
	return AdapterExpectation{}
}

func TestSemanticIDLTemplatesMatchRealExtractors(t *testing.T) {
	profile, err := mapFrozenProfile("semantic")
	if err != nil {
		t.Fatal(err)
	}
	var start uint64
	for _, family := range profile.Families {
		if family.Plane != "idl" {
			continue
		}
		familyStart := start
		start += family.Inputs
		for _, endpoint := range []struct {
			name    string
			ordinal uint64
		}{{"first", familyStart}, {"last", start - 1}} {
			t.Run(family.Name+"/"+endpoint.name, func(t *testing.T) {
				content, err := idlContent(profile, family, endpoint.ordinal)
				if err != nil {
					t.Fatal(err)
				}
				path := "semantic/idl/b" + leftPad(endpoint.ordinal/1_024, 3) + "/f" + leftPad(endpoint.ordinal, 5) + family.Suffix
				corpus := semanticCorpus{path: path, content: string(content)}
				extractor := thriftdecl.New()
				if family.Suffix == ".proto" {
					extractor = protodecl.New()
				}
				var facts []sdk.Fact
				coverage, err := extractor.Extract(t.Context(), corpus, func(fact sdk.Fact) error {
					facts = append(facts, fact)
					return nil
				})
				if err != nil {
					t.Fatalf("%v\n%s", err, content[:min(len(content), 220)])
				}
				if coverage.UnresolvedCount != 0 {
					t.Fatalf("file-level unresolved count = %d", coverage.UnresolvedCount)
				}
				if uint64(len(facts)) != family.RecordsPerInput {
					t.Fatalf("facts = %d, want %d", len(facts), family.RecordsPerInput)
				}
				var classified uint64
				for _, fact := range facts {
					if strings.Contains(fact.Assertion.Detail, `"reason":"`+family.Reason+`"`) {
						classified++
					}
				}
				if classified != family.GapsPerInput {
					t.Fatalf("classified gaps = %d, want %d; facts=%+v", classified, family.GapsPerInput, facts)
				}
				canonical, err := json.Marshal(facts)
				if err != nil {
					t.Fatal(err)
				}
				if uint64(len(canonical)) != family.CanonicalBytesPerInput {
					t.Fatalf("canonical bytes = %d, want %d", len(canonical), family.CanonicalBytesPerInput)
				}
				if family.StagingRowsPerInput != uint64(len(facts))*2 {
					t.Fatalf("staging rows = %d, want fact association+assertion rows = %d", family.StagingRowsPerInput, len(facts)*2)
				}
			})
		}
	}
}

func leftPad(value uint64, width int) string {
	text := fmt.Sprintf("%0*d", width, value)
	return text
}

func TestPackageLessProtoIsSupportedNotAGap(t *testing.T) {
	corpus := semanticCorpus{path: "packageless.proto", content: "syntax = \"proto3\";\nmessage Accepted {}\n"}
	var facts []sdk.Fact
	coverage, err := protodecl.New().Extract(t.Context(), corpus, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 0 || len(facts) != 1 || strings.Contains(facts[0].Assertion.Detail, `"reason"`) {
		t.Fatalf("package-less proto result = coverage %+v facts %+v", coverage, facts)
	}
}

type semanticCorpus struct {
	path    string
	content string
}

func (corpus semanticCorpus) RepoName() string { return RepositoryName }
func (corpus semanticCorpus) Commit() string   { return strings.Repeat("1", 40) }

func (corpus semanticCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(corpus.path)
}

func (corpus semanticCorpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	if path != corpus.path {
		return sdk.Blob{}, context.Canceled
	}
	sum := sha256.Sum256([]byte(corpus.content))
	return sdk.Blob{Content: corpus.content, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
