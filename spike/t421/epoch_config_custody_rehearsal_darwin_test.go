//go:build darwin

package t421

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// The shared-author rehearsal supplies genuine protected inputs, not a second
// build fixture or a fabricated issuer. The returned function only registers
// exact cleanup and must run after the shared parent's actual close gate.
// Sequential native reservations below prove no server bind or readiness.
func rehearseExecutionEpochConfigs(t *testing.T, ctx context.Context, author *ExecutionAuthorCustody) func() {
	t.Helper()
	started := time.Now()
	custody, err := PrepareExecutionEpochConfigs(ctx, author)
	completed := false
	defer func() {
		if custody != nil {
			if !completed {
				t.Logf("retained exact epoch configuration custody (no automatic retry): %v", custody.RetainedPaths())
			}
			// The explicit success-path Close below has already joined this
			// descriptor release. Any error here must prevent returning a
			// cleanup-registration closure, even on a constructor failure.
			if err := custody.Close(); err != nil {
				t.Fatal("epoch configuration descriptor close refused; retaining inputs", err)
			}
		}
	}()
	if err != nil || custody == nil {
		t.Fatal("actual protected epoch configuration construction refused", err)
	}
	_, rawPlan, err := readAuthorCustodyPlan(ctx, author.request.Plan)
	var plan Plan
	if err != nil || SHA256(rawPlan) != author.planSHA256 || json.Unmarshal(rawPlan, &plan) != nil ||
		plan.Schema != PlanV3Schema || len(plan.Revisions.Logical) != 3 {
		t.Fatal("genuine protected author plan changed")
	}
	// The author already decoded/admitted these exact protected bytes. Do not
	// regenerate the physical corpus again merely to inspect catalog identities.
	source := productionSourceURL(author.roots[1].path)
	repository, err := phebssync.RepoName(source)
	if err != nil {
		t.Fatal("actual watched source identity unavailable")
	}
	paths := custody.RetainedPaths()
	if len(paths) != 6 {
		t.Fatal("epoch custody did not retain exactly four writable and two protected roots")
	}
	for index, path := range paths {
		if filepath.Dir(path) != author.parent || slices.Contains(paths[:index], path) {
			t.Fatal("epoch custody acquired an unexpected or duplicate root")
		}
	}
	var selections [5]ExecutionEpochConfig
	var addresses []string
	var catalogPaths []string
	var catalogBytes, configBytes uint64
	for index := range selections {
		epoch, err := custody.Check(ctx, uint64(index+1))
		logical := []string{"a", "b", "a-return", "a-return", "a-return"}[index]
		if err != nil || epoch.Epoch != uint64(index+1) || epoch.LogicalRevision != logical || epoch.Repository != repository ||
			slices.Contains(addresses, epoch.Listen) {
			t.Fatal("actual epoch selection or distinct reservation differs", index+1, err)
		}
		if index > 0 && (epoch.DataRoot != selections[0].DataRoot || epoch.Home != selections[0].Home ||
			epoch.Temporary != selections[0].Temporary || epoch.BackupRoot != selections[0].BackupRoot || epoch.APIKey != selections[0].APIKey) {
			t.Fatal("epochs do not share the generated roots/key")
		}
		selections[index] = epoch
		addresses = append(addresses, epoch.Listen)
		input := readEpochRehearsalInput(t, epoch.ConfigPath, maxEpochConfigBytes)
		parsed, err := config.ParseLiteral(input)
		if err != nil || SHA256(input) != epoch.ConfigSHA256 || parsed.Server.Addr != epoch.Listen || parsed.Server.DataDir != epoch.DataRoot ||
			len(parsed.Connections) != 1 || parsed.Connections[0].URL != source || !parsed.Connections[0].Watch ||
			parsed.Auth.APIKey != epoch.APIKey || parsed.ServiceCatalogs[repository].Path != epoch.CatalogPath ||
			validateEpochConfigBytes(plan, epoch, source, input) != nil {
			t.Fatal("actual protected config bytes or source/catalog binding differ", index+1, err)
		}
		configBytes += uint64(len(input))
		t.Logf("actual protected epoch %d config: %d bytes", index+1, len(input))
		if !slices.Contains(catalogPaths, epoch.CatalogPath) {
			catalogPaths = append(catalogPaths, epoch.CatalogPath)
			input := readEpochRehearsalInput(t, epoch.CatalogPath, maxEpochCatalogBytes)
			var catalog servicecatalog.Catalog
			logicalIndex := slices.Index([]string{"a", "b", "a-return"}, logical)
			want := plan.Revisions.Logical[logicalIndex].CatalogSource
			digest := SHA256(input)
			if json.Unmarshal(input, &catalog) != nil || digest != epoch.CatalogSHA256 ||
				digest != want.SHA256 || uint64(len(input)) != want.Bytes ||
				uint64(len(catalog.Services)+len(catalog.Memberships)+len(catalog.Unowned)) != want.Records {
				t.Fatal("actual protected catalog differs from genuine author plan", logical)
			}
			catalogBytes += uint64(len(input))
		}
		if probe, err := net.Listen("tcp4", epoch.Listen); err == nil {
			_ = probe.Close()
			t.Fatal("held epoch listener did not reserve its address", index+1)
		}
	}
	if len(catalogPaths) != 3 {
		t.Fatal("epoch selection did not reuse exactly three protected catalogs")
	}
	// Reading the restore configuration above must not consume its fifth
	// reservation. Release consumes each genuine native listener only once.
	for index, epoch := range selections {
		if custody.ReleaseListener(ctx, epoch.Epoch) != nil {
			t.Fatal("sequential native listener release refused", epoch.Epoch)
		}
		probe, err := net.Listen("tcp4", epoch.Listen)
		if err != nil {
			t.Fatal("released address was not available to a native bind; no retry", epoch.Epoch, err)
		}
		if probe.Close() != nil {
			t.Fatal("native bind probe did not close", epoch.Epoch)
		}
		for later := index + 1; later < len(selections); later++ {
			if _, err := custody.Check(ctx, selections[later].Epoch); err != nil {
				t.Fatal("release invalidated a later configuration reservation", err)
			}
			if probe, err := net.Listen("tcp4", selections[later].Listen); err == nil {
				_ = probe.Close()
				t.Fatal("release lost a later native reservation")
			}
		}
	}
	if custody.Close() != nil {
		t.Fatal("completed epoch configuration custody did not close")
	}
	author.mu.Lock()
	err = author.check(ctx)
	author.mu.Unlock()
	if err != nil {
		t.Fatal("epoch close released borrowed author/source/tool custody")
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("FD-only close removed retained epoch custody", err)
		}
	}
	t.Logf("actual epoch constructor/protected bytes/listener mechanics ONLY: %s; catalogs=%d config=%d bytes; no native server start or readiness claim",
		time.Since(started), catalogBytes, configBytes)
	completed = true
	return func() {
		inputCustodyTestCleanup(t, custody.catalogs, []ExecutionInputCopy{{Name: "catalog-a"}, {Name: "catalog-b"}, {Name: "catalog-a-return"}})
		inputCustodyTestCleanup(t, custody.configs, []ExecutionInputCopy{{Name: "config-1"}, {Name: "config-2"}, {Name: "config-3"}, {Name: "config-4"}, {Name: "config-5"}})
	}
}

func readEpochRehearsalInput(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		t.Fatal("protected epoch input could not be opened", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatal("protected epoch input descriptor did not close; retaining custody", err)
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit || !inputCustodyProtected(info) {
		t.Fatal("protected epoch input has invalid native metadata")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) != info.Size() {
		t.Fatal("protected epoch input read was not complete", err)
	}
	if writer, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_ = writer.Close()
		t.Fatal("protected epoch input admitted a new writable descriptor")
	}
	return raw
}
