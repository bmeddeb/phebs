package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
)

type shardHashContext struct {
	context.Context
	err func() error
}

func (c shardHashContext) Err() error { return c.err() }

func TestShardFileHashBoundaries(t *testing.T) {
	for _, test := range []string{"valid", "canceled", "truncated", "empty", "oversize"} {
		t.Run(test, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shard")
			content := []byte("0123456789")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			ctx := t.Context()
			size := int64(len(content))
			var want error
			switch test {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "truncated":
				calls := 0
				ctx = shardHashContext{Context: ctx, err: func() error {
					calls++
					if calls == 2 {
						if err := os.Truncate(path, 4); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				}}
				want = io.ErrUnexpectedEOF
			case "empty":
				size = 0
			case "oversize":
				size = math.MaxUint32
			}
			digest, err := digestShardFileContext(ctx, file, size, make([]byte, 4))
			if test == "valid" {
				if expected := fmt.Sprintf("sha256:%x", sha256.Sum256(content)); err != nil || digest != expected {
					t.Fatalf("hash = %q, %v; want %q", digest, err, expected)
				}
			} else if err == nil || digest != "" || (want != nil && !strings.Contains(err.Error(), want.Error())) {
				t.Fatalf("refusal = %q, %v; want %v", digest, err, want)
			}
		})
	}
}

func TestStaticShardHashBindsOriginalFile(t *testing.T) {
	for _, test := range []string{"valid", "digest-mismatch", "restored-mtime-write", "replaced-path", "canceled-after-hash"} {
		t.Run(test, func(t *testing.T) {
			dir := t.TempDir()
			builder, err := index.NewBuilder(index.Options{
				IndexDir: dir, ShardPrefixOverride: "bound", Parallelism: 1, DisableCTags: true,
				RepositoryDescription: zoekt.Repository{Name: "bound"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.Add(index.Document{Name: "main.go", Content: []byte("package bound\nconst Needle = true\n")}); err != nil {
				t.Fatal(err)
			}
			if err := builder.Finish(); err != nil {
				t.Fatal(err)
			}
			paths, err := filepath.Glob(filepath.Join(dir, "*.zoekt"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("fixture members = %v, %v", paths, err)
			}
			path := paths[0]
			content, err := os.ReadFile(path)
			if err != nil || len(content) >= 128<<10 {
				t.Fatalf("fixture bytes=%d, %v", len(content), err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
			if test == "digest-mismatch" {
				digest = "sha256:" + strings.Repeat("0", 64)
			}
			calls, mutated := 0, false
			ctx := shardHashContext{Context: t.Context(), err: func() error {
				calls++
				// The fourth check follows the final buffered read and precedes
				// descriptor revalidation/mmap. No production test hook is needed.
				if calls != 4 {
					return nil
				}
				switch test {
				case "restored-mtime-write":
					if err := os.WriteFile(path, content, before.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
					mutated = true
				case "replaced-path":
					if err := os.WriteFile(path+".replacement", content, before.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(path+".replacement", path); err != nil {
						t.Fatal(err)
					}
					mutated = true
				case "canceled-after-hash":
					mutated = true
					return context.Canceled
				}
				return nil
			}}
			searcher, err := materializeStaticShards(ctx, dir, "whole", "bound", []staticShardDescriptor{{
				name: filepath.Base(path), contentDigest: digest,
			}})
			if test != "valid" {
				if searcher != nil {
					searcher.Close()
				}
				if err == nil || searcher != nil || (test != "digest-mismatch" && !mutated) {
					t.Fatalf("changed shard admitted: searcher=%v err=%v mutation=%v", searcher, err, mutated)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer searcher.Close()
			result, err := searcher.Search(t.Context(), &query.Substring{Pattern: "Needle", Content: true}, Options{}.zoekt())
			if err != nil || len(result.Files) != 1 {
				t.Fatalf("validated shard query = %+v, %v", result, err)
			}
		})
	}
}
