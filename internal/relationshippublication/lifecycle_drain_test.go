package relationshippublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real flat files exercise the shared collector. Component builders are not
// run here; existing publication fixtures separately establish their formats.
func TestLifecycleSharedComponentsLargeBoundedDrain(t *testing.T) {
	for _, budget := range []int{16, 1024} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			dataDir := t.TempDir()
			hash := repositoryHash("example.com/acme/shared-drain")
			refs := newComponentReferenceSet()
			old, live, pointer := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
			components := []struct {
				path, prefix string
				files        int
				references   map[string]struct{}
			}{
				{"relationship-resolver-namespaces/resolver-namespaces", "generation-", 10001, refs.resolver},
				{"relationship-rpc-postings/rpc-caller-postings", "", 257, refs.rpc},
				{"relationship-kafka-postings/kafka-topic-postings", "", 447, refs.kafka},
			}
			var protected, collecting []string
			wantDeleted, wantTurns := 0, 0
			for _, component := range components {
				base := filepath.Join(dataDir, component.path, hash)
				seedLifecycleFlatFiles(t, filepath.Join(base, component.prefix+old), component.files)
				liveDir := filepath.Join(base, component.prefix+live)
				seedLifecycleFlatFiles(t, liveDir, 1)
				protected = append(protected, filepath.Join(liveDir, "00000.json"))
				component.references["sha256:"+live] = struct{}{}
				collecting = append(collecting, filepath.Join(base, "collecting-"+component.prefix+old))
				wantDeleted += component.files + 1
				wantTurns += (component.files + 1 + budget - 1) / budget
				if component.prefix != "" {
					pointerDir := filepath.Join(base, component.prefix+pointer)
					seedLifecycleFlatFiles(t, pointerDir, 1)
					protected = append(protected, filepath.Join(pointerDir, "00000.json"))
					writeLifecycleFixtureFile(t, filepath.Join(base, "current.json"), []byte(fmt.Sprintf("{\"generation_digest\":\"sha256:%s\"}", pointer)))
				}
			}
			deleted := 0
			for turn := 0; turn < wantTurns; turn++ {
				count, _, scanned, err := sweepOrphanComponent(t.Context(), dataDir, hash, refs, budget)
				if err != nil || count < 1 || count > budget || scanned != 1 {
					t.Fatalf("turn %d: deleted=%d scanned=%d err=%v", turn, count, scanned, err)
				}
				deleted += count
			}
			if deleted != wantDeleted {
				t.Fatalf("deleted=%d want=%d", deleted, wantDeleted)
			}
			for _, directory := range collecting {
				if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("collecting remains: %s: %v", directory, err)
				}
			}
			for _, path := range protected {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("protected component changed: %s: %v", path, err)
				}
			}
			count, more, scanned, err := sweepOrphanComponent(t.Context(), dataDir, hash, refs, budget)
			if err != nil || count != 0 || more || scanned != 0 {
				t.Fatalf("drained confirmation: %d %t %d %v", count, more, scanned, err)
			}
			t.Logf("actual files+directories=%d collector turns=%d delete budget=%d", deleted, wantTurns, budget)
		})
	}
}

func TestLifecycleFlatDrainSafetyAndPrefix(t *testing.T) {
	for _, kind := range []string{"late_symlink", "late_directory", "canceled", "partial_cancel", "inventory_excess", "zero_budget"} {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "generation")
			seedLifecycleFlatFiles(t, directory, 2)
			ctx := t.Context()
			budget, inventory := 16, MaxStageRepairFiles
			wantDeleted, wantError := 0, ErrInvalid
			switch kind {
			case "late_symlink":
				target := filepath.Join(t.TempDir(), "precious")
				writeLifecycleFixtureFile(t, target, []byte("preserve"))
				if err := os.Symlink(target, filepath.Join(directory, "z-invalid")); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if raw, err := os.ReadFile(target); err != nil || string(raw) != "preserve" {
						t.Errorf("symlink target changed: %q %v", raw, err)
					}
				})
				wantDeleted = 2
			case "late_directory":
				if err := os.Mkdir(filepath.Join(directory, "z-invalid"), 0o700); err != nil {
					t.Fatal(err)
				}
				wantDeleted = 2
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantError = context.Canceled
			case "partial_cancel":
				parent, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = &cancelAfterLifecycleUnlink{Context: parent, path: filepath.Join(directory, "00000.json"), cancel: cancel}
				wantDeleted, wantError = 1, context.Canceled
			case "inventory_excess":
				inventory, wantError = 1, ErrLimit
			case "zero_budget":
				budget, wantError = 0, ErrLimit
			}
			deleted, complete, err := drainFlatGenerationBounded(ctx, directory, budget, inventory)
			if deleted != wantDeleted || complete || !errors.Is(err, wantError) {
				t.Fatalf("drain=%d/%t/%v want=%d/%v", deleted, complete, err, wantDeleted, wantError)
			}
			if _, err := os.Lstat(directory); err != nil {
				t.Fatalf("failed generation custody removed: %v", err)
			}
		})
	}
}

func TestLifecycleStageParentRemovalCharged(t *testing.T) {
	for _, v3 := range []bool{false, true} {
		for _, budget := range []int{1, 16, 1024} {
			t.Run(fmt.Sprintf("v3=%t/budget=%d", v3, budget), func(t *testing.T) {
				dataDir := t.TempDir()
				namespace := "relationship-publications"
				if v3 {
					namespace = RelationshipPublicationsV3Shadow
				}
				repository := filepath.Join(dataDir, "relationships", namespace, repositoryHash("example.com/acme/stage"))
				seedLifecycleFlatFiles(t, filepath.Join(repository, ".stage-crash"), budget-1)
				sweep := SweepLifecycle
				if v3 {
					sweep = SweepLifecycleV3
				}
				result, err := sweep(t.Context(), dataDir, time.Now(), "", &CacheV3{}, budget)
				if err != nil || result.Deleted != budget || !result.More {
					t.Fatalf("stage closure=%+v %v", result, err)
				}
				result, err = sweep(t.Context(), dataDir, time.Now(), result.Cursor, &CacheV3{}, budget)
				if err != nil || result.Deleted != 1 || !result.More {
					t.Fatalf("deferred repository closure=%+v %v", result, err)
				}
				if _, err := os.Lstat(repository); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("empty repository remains: %v", err)
				}
				result, err = sweep(t.Context(), dataDir, time.Now(), result.Cursor, &CacheV3{}, budget)
				if err != nil || result.Deleted != 0 || result.More {
					t.Fatalf("final confirmation=%+v %v", result, err)
				}
			})
		}
	}
}

func TestLifecycleFlatInventoryBoundary(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "generation")
	seedLifecycleFlatFiles(t, directory, MaxStageRepairFiles+1)
	deleted, complete, err := drainFlatGeneration(t.Context(), directory, 16)
	if deleted != 0 || complete || !errors.Is(err, ErrLimit) {
		t.Fatalf("one-over inventory mutated: %d %t %v", deleted, complete, err)
	}
	if err := os.Remove(filepath.Join(directory, fmt.Sprintf("%05d.json", MaxStageRepairFiles))); err != nil {
		t.Fatal(err)
	}
	deleted, complete, err = drainFlatGeneration(t.Context(), directory, 16)
	if deleted != 16 || complete || err != nil {
		t.Fatalf("exact inventory does not progress: %d %t %v", deleted, complete, err)
	}
}

func TestLifecycleV3ConfirmsAllOrphanComponents(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/component-confirmation"
	current := publishLifecycleGenerationV3(t, root, repository, "1", "current-run", &lifecyclePinStoreV3{})
	hash := repositoryHash(repository)
	components := []string{
		filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", hash, "generation-"+strings.Repeat("e", 64)),
		filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings", hash, strings.Repeat("e", 64)),
		filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings", hash, strings.Repeat("e", 64)),
	}
	for _, directory := range components {
		seedLifecycleFlatFiles(t, directory, 1)
	}
	for index := range components {
		result, err := SweepLifecycleV3(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
		if err != nil || result.Deleted != 2 || !result.More {
			t.Fatalf("component%d prematurely exact=%+v %v", index, result, err)
		}
		if index+1 < len(components) {
			if _, err := os.Lstat(components[index+1]); err != nil {
				t.Fatalf("later component missing: %v", err)
			}
		}
	}
	result, err := SweepLifecycleV3(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
	if err != nil || result.Deleted != 0 || result.More {
		t.Fatalf("final component confirmation=%+v %v", result, err)
	}
	if _, err := os.Lstat(mustGenerationPathV3(t, root, repository, current.GenerationDigest)); err != nil {
		t.Fatalf("current generation changed: %v", err)
	}
}

func TestLifecycleRecoveryDrainRetainsChargedPrefix(t *testing.T) {
	for _, stage := range []bool{false, true} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			directory := t.TempDir()
			name := strings.Repeat("a", 64)
			if stage {
				name = ".stage-crash"
			}
			generation := filepath.Join(directory, name)
			seedLifecycleFlatFiles(t, generation, 18)
			var work int
			var err error
			if stage {
				_, work, err = repairStageDirectories(t.Context(), directory, 3)
			} else {
				work, err = cleanupOrphanComponentNamespace(t.Context(), directory, false, 3)
			}
			if work != 3 || !errors.Is(err, ErrLimit) {
				t.Fatalf("charged prefix=%d %v", work, err)
			}
			entries, err := os.ReadDir(generation)
			if err != nil || len(entries) != 16 {
				t.Fatalf("retained generation=%d %v", len(entries), err)
			}
		})
	}
}

func TestLifecycleStageRetainsPositiveErrorPrefix(t *testing.T) {
	for _, v3 := range []bool{false, true} {
		t.Run(fmt.Sprint(v3), func(t *testing.T) {
			dataDir := t.TempDir()
			namespace := "relationship-publications"
			if v3 {
				namespace = RelationshipPublicationsV3Shadow
			}
			stage := filepath.Join(dataDir, "relationships", namespace, repositoryHash("example.com/acme/prefix"), ".stage-crash")
			seedLifecycleFlatFiles(t, stage, 2)
			if err := os.Mkdir(filepath.Join(stage, "z-invalid"), 0o700); err != nil {
				t.Fatal(err)
			}
			sweep := SweepLifecycle
			if v3 {
				sweep = SweepLifecycleV3
			}
			result, err := sweep(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
			if !errors.Is(err, ErrInvalid) || result.Deleted != 2 || result.Scanned != 1 || !result.More {
				t.Fatalf("sweep prefix=%+v %v", result, err)
			}
		})
	}
}

func TestLifecycleEmptyRepositoryKeepsSharedDiscovery(t *testing.T) {
	for _, v3 := range []bool{false, true} {
		t.Run(fmt.Sprint(v3), func(t *testing.T) {
			dataDir := t.TempDir()
			namespace := "relationship-publications"
			sweep := SweepLifecycle
			if v3 {
				namespace, sweep = RelationshipPublicationsV3Shadow, SweepLifecycleV3
			}
			hash := repositoryHash("example.com/acme/empty-discovery")
			repository := filepath.Join(dataDir, "relationships", namespace, hash)
			seedLifecycleFlatFiles(t, filepath.Join(repository, ".stage-crash"), 1)
			for _, component := range []string{"relationship-resolver-namespaces/resolver-namespaces", "relationship-rpc-postings/rpc-caller-postings", "relationship-kafka-postings/kafka-topic-postings"} {
				name := strings.Repeat("a", 64)
				if strings.Contains(component, "resolver") {
					name = "generation-" + name
				}
				seedLifecycleFlatFiles(t, filepath.Join(dataDir, component, hash, name), 2)
			}
			for turn, wantDeleted := range []int{2, 3, 3, 3} {
				result, err := sweep(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
				if err != nil || result.Deleted != wantDeleted || !result.More {
					t.Fatalf("turn%d=%+v %v", turn, result, err)
				}
				if _, err := os.Lstat(repository); err != nil {
					t.Fatalf("shared discovery lost: %v", err)
				}
			}
			result, err := sweep(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
			if err != nil || result.Deleted != 1 || !result.More {
				t.Fatalf("discovery removal=%+v %v", result, err)
			}
			result, err = sweep(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
			if err != nil || result.Deleted != 0 || result.More {
				t.Fatalf("drained confirmation=%+v %v", result, err)
			}
		})
	}
}

func TestLifecycleEmptyLegacyRepositoryProtectsV3Components(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/empty-legacy"
	current := publishLifecycleGenerationV3(t, root, repository, "1", "current", &lifecyclePinStoreV3{})
	hash := repositoryHash(repository)
	legacy := filepath.Join(root, "relationship-publications", hash)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", hash,
		"generation-"+strings.TrimPrefix(current.Authority.ResolverGenerationDigest, "sha256:"))
	seedLifecycleFlatFiles(t, protected, 1)
	result, err := SweepLifecycle(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 16)
	if err != nil || result.Deleted != 1 || !result.More {
		t.Fatalf("empty legacy=%+v %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(protected, "00000.json")); err != nil {
		t.Fatalf("V3 retained component deleted: %v", err)
	}
}

func TestLifecycleUnpinStageTransitionResumes(t *testing.T) {
	for _, mode := range []string{"one_delete", "cancel_after_marker", "collision", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			name := strings.Repeat("b", 64)
			collecting := filepath.Join(parent, "collecting-"+name)
			stage := filepath.Join(parent, ".stage-unpinned-"+name)
			if err := os.Mkdir(collecting, 0o700); err != nil {
				t.Fatal(err)
			}
			writeLifecycleFixtureFile(t, filepath.Join(collecting, collectionUnpinnedName), nil)
			ctx, budget := t.Context(), 1
			if mode == "collision" {
				seedLifecycleFlatFiles(t, stage, 1)
			}
			if mode == "cancel_after_marker" {
				parentCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = &cancelAfterLifecycleUnlink{Context: parentCtx, path: filepath.Join(stage, collectionUnpinnedName), requireDirectory: stage, cancel: cancel}
				budget = 2
			}
			deleted, complete, err := drainUnpinnedCollection(ctx, collecting, budget)
			if mode == "collision" {
				if deleted != 0 || complete || !errors.Is(err, ErrInvalid) {
					t.Fatalf("collision=%d/%t/%v", deleted, complete, err)
				}
				if unpinned, err := collectionUnpinned(collecting); err != nil || !unpinned {
					t.Fatalf("collision lost proof: %t %v", unpinned, err)
				}
				if _, err := os.Lstat(filepath.Join(stage, "00000.json")); err != nil {
					t.Fatalf("collision target changed: %v", err)
				}
				return
			}
			if deleted != 1 || complete || (mode == "cancel_after_marker" && !errors.Is(err, context.Canceled)) || (mode != "cancel_after_marker" && err != nil) {
				t.Fatalf("marker prefix=%d/%t/%v", deleted, complete, err)
			}
			if _, err := os.Lstat(collecting); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old collecting remains: %v", err)
			}
			if mode == "recovery" {
				removed, work, err := repairStageDirectories(t.Context(), parent, 3)
				if err != nil || !removed || work != 3 {
					t.Fatalf("restart stage recovery=%t/%d/%v", removed, work, err)
				}
				return
			}
			// Re-enter through the actual ordinary stage-discovery route.
			dataDir := t.TempDir()
			repo := filepath.Join(dataDir, "relationships", RelationshipPublicationsV3Shadow, repositoryHash("example.com/acme/unpin-resume"))
			if err := os.MkdirAll(filepath.Dir(repo), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(parent, repo); err != nil {
				t.Fatal(err)
			}
			result, err := SweepLifecycleV3(t.Context(), dataDir, time.Now(), "", &CacheV3{}, 1)
			if err != nil || result.Deleted != 1 || !result.More {
				t.Fatalf("resumed stage=%+v %v", result, err)
			}
		})
	}
}

type cancelAfterLifecycleUnlink struct {
	context.Context
	path             string
	requireDirectory string
	cancel           context.CancelFunc
}

func (ctx *cancelAfterLifecycleUnlink) Err() error {
	if ctx.requireDirectory != "" {
		if _, err := os.Lstat(ctx.requireDirectory); err != nil {
			return ctx.Context.Err()
		}
	}
	if _, err := os.Lstat(ctx.path); errors.Is(err, os.ErrNotExist) {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func seedLifecycleFlatFiles(t *testing.T, directory string, count int) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := range count {
		writeLifecycleFixtureFile(t, filepath.Join(directory, fmt.Sprintf("%05d.json", index)), nil)
	}
}

func writeLifecycleFixtureFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
