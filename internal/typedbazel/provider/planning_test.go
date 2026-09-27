package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"google.golang.org/protobuf/encoding/protowire"
)

func pb(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func pi(n protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
}
func join(b ...[]byte) []byte { return bytes.Join(b, nil) }
func TestPlannerThreeCommandSequence(t *testing.T) {
	h := strings.Repeat("a", 64)
	owner := "@@//lib:lib"
	cq := protowire.AppendBytes(nil, pb(2, join(pi(1, 1), pb(4, []byte(h)))))
	rule := join(pb(1, []byte(owner)), pb(2, []byte("go_library")), pb(15, pb(1, []byte("@@//lib:lib.go"))))
	cq = append(cq, protowire.AppendBytes(nil, pb(1, join(pb(1, join(pi(1, 1), pb(2, rule))), pi(3, 1))))...)
	sourceTarget := pb(1, join(pi(1, 2), pb(3, pb(1, []byte("@@//lib:lib.go")))))
	cq = append(cq, protowire.AppendBytes(nil, pb(1, sourceTarget))...)
	aq := join(pb(5, join(pi(1, 1), pb(4, []byte(h)))), pb(6, join(pi(1, 1), pb(2, []byte("@@//phebs_plan:aspect.bzl%phebs_plan")))), pb(3, join(pi(1, 1), pb(2, []byte(owner)))))
	parts := []string{"bazel-out", "cfg", "bin", "lib", "lib.phebs-plan.json"}
	for j, s := range parts {
		aq = append(aq, pb(8, join(pi(1, uint64(j+1)), pb(2, []byte(s)), pi(3, uint64(j))))...)
	}
	aq = append(aq, pb(1, join(pi(1, 1), pi(2, uint64(len(parts)))))...)
	aq = append(aq, pb(2, join(pi(1, 1), pi(2, 1), pb(4, []byte("PhebsPlan")), pi(5, 1), pi(9, 1)))...)
	f := map[string]any{"path": "lib/lib.go", "short_path": "lib/lib.go", "owner": "@@//lib:lib.go", "source": true, "tree": false, "sha256": strings.TrimPrefix(hash([]byte("package lib\n")), "sha256:"), "bytes": 12}
	projection, _ := json.Marshal(map[string]any{"version": planner.ProjectionV2, "owner": owner, "roots": []string{"bazel-out/cfg/bin/lib/lib.x"}, "embeds": []string{}, "archives": []any{map[string]any{"name": "lib", "label": owner, "export": "bazel-out/cfg/bin/lib/lib.x", "import_path": "example.test/lib", "import_map": "example.test/lib", "go_files": []any{f}, "compiled_go_files": []any{f}, "other_files": []any{}, "imports": []any{}, "package_name": "lib", "source_imports": []string{}, "mode": planner.GoMode{GOOS: "linux", GOARCH: "arm64"}}}})
	events := []string{}
	command := func(_ context.Context, exe string, args, env []string, b *outputBudget) ([]byte, []byte, error) {
		if exe != "/inputs/tools/bin/bazel" || !slices.Equal(env, launcher.BazelEnvironment()) || !slices.Contains(args, "--repository_disable_download") || !slices.Contains(args, "--lockfile_mode=error") || !slices.Contains(args, "--ignore_all_rc_files") || !slices.Contains(args, "--experimental_proto_descriptor_sets_include_source_info") {
			t.Fatal("unclosed recipe", args)
		}
		switch {
		case slices.Contains(args, "cquery"):
			events = append(events, "cquery")
			return cq, nil, nil
		case slices.Contains(args, "aquery"):
			events = append(events, "aquery")
			return aq, nil, nil
		case slices.Contains(args, "build"):
			events = append(events, "build")
			return nil, nil, nil
		}
		t.Fatal("command shape")
		return nil, nil, nil
	}
	p, e := buildPlan(context.Background(), []string{"//lib:lib"}, command, func() error { events = append(events, "quiesce"); return nil }, func(name string) (CacheEviction, error) {
		if name != gazelleCompilerCache {
			t.Fatal(name)
		}
		events = append(events, "evict")
		return CacheEviction{}, nil
	}, func(name string, limit int64) ([]byte, error) {
		if name != strings.Join(parts, "/") || limit != planner.MaxProjectionBytes {
			t.Fatal(name, limit)
		}
		events = append(events, "projection")
		return projection, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if p.Version != planner.PlanV2 || len(p.Units) != 1 || len(p.Documents) != 1 || !slices.Equal(events, []string{"cquery", "aquery", "quiesce", "evict", "build", "projection"}) {
		t.Fatal(p, events)
	}
}
func TestCommandHelper(t *testing.T) {
	switch os.Getenv("PHEBS_PROVIDER_TEST") {
	case "output":
		_, _ = os.Stdout.Write([]byte("1234"))
		_, _ = os.Stderr.Write([]byte("5678"))
		os.Exit(0)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}
func TestRealCommandBoundsAndCancellation(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, bound := range []int{8, 7} {
		out, errout, e := runCommandAt(context.Background(), exe, []string{"-test.run=^TestCommandHelper$"}, []string{"PHEBS_PROVIDER_TEST=output"}, &outputBudget{remaining: bound}, t.TempDir())
		if bound == 8 {
			if e != nil || len(out)+len(errout) != 8 {
				t.Fatal(e, string(out), string(errout))
			}
		} else if !errors.Is(e, typedindex.Capacity) {
			t.Fatal("aggregate limit accepted")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, e = runCommandAt(ctx, exe, []string{"-test.run=^TestCommandHelper$"}, []string{"PHEBS_PROVIDER_TEST=wait"}, &outputBudget{remaining: 8}, t.TempDir())
	if !errors.Is(e, context.DeadlineExceeded) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
