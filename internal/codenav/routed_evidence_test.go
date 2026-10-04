package codenav_test

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func TestRoutedGeneratedNavigationKeepsCommittedEvidenceIndependent(t *testing.T) {
	for _, posture := range []string{"absent", "valid", "corrupt"} {
		t.Run(posture, func(t *testing.T) {
			navigation, query, dataDir, committed, costs := codenav.NewRoutedEvidenceFixture(t, posture)
			definition, err := navigation.Definition(t.Context(), query)
			if err != nil || !definition.Available || definition.Location == nil ||
				definition.Location.Path != "lib/rocket.go" || definition.Location.Revision != query.Revision {
				t.Fatalf("generated navigation = %+v, %v", definition, err)
			}
			before := costs()
			if before[1] == 0 || before[2] != 2 || before[3] == 0 || before[4] != 0 {
				t.Fatalf("managed navigation costs = %v", before)
			}
			factory := extract.GitCorpus(dataDir)
			unlock, err := factory.Lock(t.Context(), query.Repo)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			corpus := factory.New(query.Repo, query.Revision)
			if corpus.Commit() != query.Revision {
				t.Fatal("evidence corpus lost the immutable revision")
			}
			if err = corpus.WalkFiles(t.Context(), func(path string) error {
				if path == query.Path {
					t.Fatal("managed generated source entered the Git corpus")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			input, err := corpus.(sdk.SCIPCorpus).ReadSCIPIndex(t.Context())
			if err != nil || input.Path != "index.scip" || input.Present != (committed != nil) || input.Content != string(committed) {
				t.Fatalf("committed evidence input = %+v, %v", input, err)
			}
			if committed != nil && input.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(committed)) {
				t.Fatal("managed navigation changed the committed SCIP digest")
			}
			if committed == nil && input.Digest != "" {
				t.Fatal("absent committed SCIP acquired a managed digest")
			}
			facts := 0
			coverage, err := scipfield.New().Extract(t.Context(), corpus, func(sdk.Fact) error {
				facts++
				return nil
			})
			switch posture {
			case "absent":
				if err != nil || !slices.Equal(coverage.Protocols, []string{"scip-index-absent"}) {
					t.Fatalf("absent committed evidence coverage = %+v, %v", coverage, err)
				}
			case "valid":
				if err != nil || !slices.Contains(coverage.Protocols, "scip") || slices.Contains(coverage.Protocols, "scip-index-absent") {
					t.Fatalf("valid committed evidence coverage = %+v, %v", coverage, err)
				}
			case "corrupt":
				if err == nil || !strings.Contains(err.Error(), "parse index.scip:") {
					t.Fatalf("corrupt committed evidence was not refused: %v", err)
				}
			}
			if facts != 0 {
				t.Fatalf("navigation-only fixture acquired %d protobuf-field facts", facts)
			}
			if after := costs(); after != before {
				t.Fatalf("evidence touched managed navigation: before=%v after=%v", before, after)
			}
			definition, err = navigation.Definition(t.Context(), query)
			if err != nil || !definition.Available || definition.Location == nil || costs()[4] != 0 {
				t.Fatalf("evidence changed generated navigation or leaked pins: %+v, %v", definition, err)
			}
		})
	}
}
