package resolvercatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

func TestOpenMarkedIsRepositoryScoped(t *testing.T) {
	targetPacks := []ResolverPack{
		{Name: "go-module", Version: "1.0.0"},
		{Name: "grpc-generated", Version: "1.0.0"},
	}
	tests := []struct {
		name            string
		expectedPacks   []ResolverPack
		wantError       bool
		wantMemberOpens int
	}{
		{
			name: "exact pack set",
			expectedPacks: []ResolverPack{
				{Name: "go-module", Version: "1.0.0"},
				{Name: "grpc-generated", Version: "1.0.0"},
			},
			wantMemberOpens: 1,
		},
		{
			name: "pack mismatch fails before member open",
			expectedPacks: []ResolverPack{
				{Name: "go-module", Version: "1.0.0"},
			},
			wantError: true,
		},
		{
			name: "unordered expected pack set fails before member open",
			expectedPacks: []ResolverPack{
				{Name: "grpc-generated", Version: "1.0.0"},
				{Name: "go-module", Version: "1.0.0"},
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "catalogs")
			repository := "github.com/acme/target"
			state := testInstallWithMembers(
				t, root, repository, targetPacks, "target.ndjson",
			)

			foreignRepository := "github.com/acme/foreign"
			foreignStage, err := NewStage(
				root, testIdentity(t, foreignRepository),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := foreignStage.AddMember(
				t.Context(), "foreign.ndjson", json.RawMessage(`{}`),
				func(write func(json.RawMessage) error) error {
					return write(json.RawMessage(
						`{"schema":"phebs-resolver-catalog-record-v1"}`,
					))
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := installMarker(root, foreignRepository); err != nil {
				t.Fatal(err)
			}
			foreignMarker := filepath.Join(
				root, resolvercatalogid.PublishingName(foreignRepository),
			)
			markerBefore, err := os.ReadFile(foreignMarker)
			if err != nil {
				t.Fatal(err)
			}
			rootBefore := testDirectoryNames(t, root)
			stageBefore := testDirectoryNames(t, foreignStage.path)

			memberOpens := 0
			testAfterStableOpen = func(opened string) {
				if strings.HasSuffix(opened, ".ndjson") {
					memberOpens++
				}
			}
			t.Cleanup(func() { testAfterStableOpen = nil })
			publication, openErr := OpenMarked(
				t.Context(), root, repository, test.expectedPacks,
			)
			if test.wantError {
				if openErr == nil || publication != nil {
					t.Fatalf("OpenMarked = %#v, %v; want error", publication, openErr)
				}
			} else {
				if openErr != nil {
					t.Fatal(openErr)
				}
				if publication == nil || !statesEqual(publication.State(), state) {
					t.Fatalf("recovered state = %+v; want %+v", publication, state)
				}
			}
			if memberOpens != test.wantMemberOpens {
				t.Fatalf("member opens = %d; want %d", memberOpens, test.wantMemberOpens)
			}
			if !IsPublishing(root, repository) {
				t.Fatal("target marker was mutated")
			}
			if got := testDirectoryNames(t, root); !slices.Equal(got, rootBefore) {
				t.Fatalf("catalog root changed: before=%v after=%v", rootBefore, got)
			}
			if got := testDirectoryNames(t, foreignStage.path); !slices.Equal(got, stageBefore) {
				t.Fatalf("foreign stage changed: before=%v after=%v", stageBefore, got)
			}
			markerAfter, err := os.ReadFile(foreignMarker)
			if err != nil || !slices.Equal(markerAfter, markerBefore) {
				t.Fatalf("foreign marker changed: bytes=%q err=%v", markerAfter, err)
			}
		})
	}
}

func TestDiscardOwnsOneBoundedUnpublishedStage(t *testing.T) {
	t.Run("stage discard is idempotent and leaves foreign stage", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		target, err := NewStage(root, testIdentity(t, "github.com/acme/target"))
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := NewStage(root, testIdentity(t, "github.com/acme/foreign"))
		if err != nil {
			t.Fatal(err)
		}
		if err := target.AddMember(t.Context(), "target.ndjson", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
		if err := foreign.AddMember(t.Context(), "foreign.ndjson", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
		targetPath, foreignPath := target.path, foreign.path
		if err := target.Discard(); err != nil {
			t.Fatal(err)
		}
		if err := target.Discard(); err != nil {
			t.Fatalf("second discard: %v", err)
		}
		if err := target.AddMember(
			t.Context(), "escaped.ndjson", json.RawMessage(`{}`), nil,
		); err == nil {
			t.Fatal("discarded stage accepted another member")
		}
		if _, err := target.Seal(t.Context()); err == nil {
			t.Fatal("discarded stage was sealed")
		}
		if _, err := os.Lstat(targetPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target stage remains: %v", err)
		}
		if got := testDirectoryNames(t, foreignPath); !slices.Equal(
			got, []string{"member-000.ndjson"},
		) {
			t.Fatalf("foreign stage changed: %v", got)
		}
	})

	t.Run("prepared discard is idempotent", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		stage, err := NewStage(root, testIdentity(t, "github.com/acme/prepared"))
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.AddMember(t.Context(), "prepared.ndjson", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		stagePath := prepared.stage
		if err := prepared.Discard(); err != nil {
			t.Fatal(err)
		}
		if err := prepared.Discard(); err != nil {
			t.Fatalf("second discard: %v", err)
		}
		if _, err := os.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("prepared stage remains: %v", err)
		}
		if err := stage.Discard(); err == nil {
			t.Fatal("sealed Stage discarded instead of preserving Prepared ownership")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "nested directory is never recursively removed",
			mutate: func(t *testing.T, stagePath string) {
				if err := os.WriteFile(
					filepath.Join(stagePath, "before"), []byte("keep"), 0o600,
				); err != nil {
					t.Fatal(err)
				}
				nested := filepath.Join(stagePath, "nested")
				if err := os.Mkdir(nested, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(nested, "sentinel"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized flat inventory is refused",
			mutate: func(t *testing.T, stagePath string) {
				for index := 0; index < MaxMembers+2; index++ {
					name := filepath.Join(stagePath, fmt.Sprintf("extra-%03d", index))
					if err := os.WriteFile(name, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "catalogs")
			stage, err := NewStage(root, testIdentity(t, "github.com/acme/damaged"))
			if err != nil {
				t.Fatal(err)
			}
			stagePath := stage.path
			test.mutate(t, stagePath)
			if err := stage.Discard(); err == nil {
				t.Fatal("damaged or oversized stage was removed")
			}
			if _, err := os.Lstat(stagePath); err != nil {
				t.Fatalf("refused stage was mutated: %v", err)
			}
			if test.name == "nested directory is never recursively removed" {
				for _, retained := range []string{"before", filepath.Join("nested", "sentinel")} {
					if _, err := os.Lstat(filepath.Join(stagePath, retained)); err != nil {
						t.Fatalf("refused stage entry %q was mutated: %v", retained, err)
					}
				}
			}
		})
	}
}

func testDirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}
