//go:build darwin

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"gopkg.in/yaml.v3"
)

func epochTestSelection(t *testing.T, index int) (ExecutionEpochConfig, string) {
	t.Helper()
	source := productionSourceURL("/private/t422-test/source")
	repository, err := phebssync.RepoName(source)
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionEpochConfig{Epoch: uint64(index + 1), LogicalRevision: []string{"a", "b", "a-return", "a-return", "a-return"}[index],
		Repository: repository, Listen: "127.0.0.1:3070", APIKey: strings.Repeat("a", 64),
		DataRoot: "/private/t422-test/data", Home: "/private/t422-test/home", Temporary: "/private/t422-test/tmp",
		BackupRoot: "/private/t422-test/backup", CatalogPath: "/private/t422-test/catalog"}, source
}

func TestExecutionEpochCatalogAndConfigExactBytes(t *testing.T) {
	plan := accountingTestPlan(t)
	inputs, err := epochCatalogInputs(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for index, input := range inputs {
		var catalog servicecatalog.Catalog
		if json.Unmarshal(input.raw, &catalog) != nil {
			t.Fatal("catalog is not canonical JSON")
		}
		want := plan.Revisions.Logical[index].CatalogSource
		if uint64(len(input.raw)) != want.Bytes || SHA256(input.raw) != want.SHA256 ||
			uint64(len(catalog.Services)+len(catalog.Memberships)+len(catalog.Unowned)) != want.Records {
			t.Fatal("generated catalog differs from existing exact frozen identity")
		}
		total += uint64(len(input.raw))
		t.Logf("%s: %d records, %d bytes", input.name, want.Records, len(input.raw))
	}
	t.Logf("all three canonical catalogs: %d bytes; no physical corpus generated", total)
	if bytes.Equal(inputs[0].raw, inputs[1].raw) || bytes.Equal(inputs[0].raw, inputs[2].raw) {
		t.Fatal("logical operator versions lost their byte identity")
	}
	var configTotal int
	for index := range 5 {
		epoch, source := epochTestSelection(t, index)
		raw, err := epochConfigBytes(plan, epoch, source)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := config.ParseLiteral(raw)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.ServiceCatalogs[epoch.Repository].Version != []string{combinedAuthorityA, combinedAuthorityB, combinedAuthorityAReturn, combinedAuthorityAReturn, combinedAuthorityAReturn}[index] ||
			parsed.Sync.PollInterval != "250ms" || !parsed.Diagnostics.Jobs || !parsed.Diagnostics.Candidates || !parsed.Diagnostics.Extraction || parsed.Diagnostics.ExtractorDetails ||
			!parsed.Lifecycle.EnabledFor() || parsed.Auth.SecureCookies() {
			t.Fatal("generated config is not the selected ordinary semantic recipe")
		}
		configTotal += len(raw)
		t.Logf("epoch %d config: %d bytes", index+1, len(raw))
	}
	t.Logf("all five fixture-path configs: %d bytes (actual private paths change byte counts)", configTotal)
}

func TestExecutionEpochConfigRefusesSemanticDrift(t *testing.T) {
	plan := accountingTestPlan(t)
	epoch, source := epochTestSelection(t, 0)
	raw, err := epochConfigBytes(plan, epoch, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"runtime", "catalog-version", "catalog-path", "poll", "watch", "details", "proto", "thrift", "thrift-field", "kafka", "workbench", "lifecycle", "permissions", "revisions", "analysis-unit", "contexts", "webhook", "audit", "cookie", "session", "source", "api-key", "address", "ambient-secret", "unknown", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := config.ParseLiteral(raw)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "runtime", "catalog-version", "catalog-path":
				selection := cfg.ServiceCatalogs[epoch.Repository]
				if mode == "runtime" {
					selection.Runtime = "v2"
				}
				if mode == "catalog-version" {
					selection.Version = combinedAuthorityB
				}
				if mode == "catalog-path" {
					selection.Path += "-different"
				}
				cfg.ServiceCatalogs[epoch.Repository] = selection
			case "poll":
				cfg.Sync.PollInterval = "1s"
			case "watch":
				cfg.Connections[0].Watch = false
			case "details":
				cfg.Diagnostics.ExtractorDetails = true
			case "proto":
				cfg.Experimental.ProvisionalProtoExtraction = false
			case "thrift":
				cfg.Experimental.ProvisionalThriftExtraction = false
			case "thrift-field":
				cfg.Experimental.ProvisionalThriftFieldExtraction = true
			case "kafka":
				cfg.Experimental.ProvisionalKafkaExtraction = false
			case "workbench":
				cfg.Experimental.ProvisionalWorkbench = true
			case "lifecycle":
				off := false
				cfg.Lifecycle.Enabled = &off
			case "permissions":
				cfg.Permissions = &config.Permissions{}
			case "revisions":
				cfg.Revisions = config.RevisionAllowlist{epoch.Repository: {"other": "refs/heads/other"}}
			case "analysis-unit":
				cfg.AnalysisUnits = map[string]analysisunit.Config{epoch.Repository: {Name: "extra", Primary: []string{"extra/"}}}
			case "contexts":
				cfg.Contexts = map[string][]string{"extra": {"*"}}
			case "webhook":
				cfg.Webhook.Secret = "extra"
			case "audit":
				cfg.Audit.Retention = "0"
			case "cookie":
				on := true
				cfg.Auth.CookieSecure = &on
			case "session":
				cfg.Auth.SessionLifetime = "1h"
			case "source":
				cfg.Connections[0].URL += "-different"
			case "api-key":
				cfg.Auth.APIKey = strings.Repeat("b", 64)
			case "address":
				cfg.Server.Addr = "127.0.0.1:3071"
			case "ambient-secret":
				cfg.Auth.APIKey = "${PRIVATE_TEST_VALUE}"
			}
			changed, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				changed = append(changed, []byte("unknown: true\n")...)
			}
			if mode == "bytes" {
				changed = bytes.Repeat([]byte{'x'}, maxEpochConfigBytes+1)
			}
			if validateEpochConfigBytes(plan, epoch, source, changed) == nil {
				t.Fatal("changed semantic input admitted")
			}
		})
	}
	for _, changed := range []ExecutionEpochConfig{{}, {Epoch: 6, LogicalRevision: "a"}, {Epoch: 1, LogicalRevision: "b"}} {
		if _, err := epochConfigBytes(plan, changed, source); err == nil {
			t.Fatal("unselected epoch admitted")
		}
	}
	plan.Profile.Pipeline.ExtractionDomains = plan.Profile.Pipeline.ExtractionDomains[:8]
	if _, err := epochConfigBytes(plan, epoch, source); err == nil {
		t.Fatal("changed domain inventory admitted")
	}
}

func TestExecutionEpochConfigRefusesMissingAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, author := range []*ExecutionAuthorCustody{nil, {}} {
		for _, selected := range []context.Context{ctx, t.Context()} {
			if custody, err := PrepareExecutionEpochConfigs(selected, author); err == nil || custody != nil {
				t.Fatal("missing genuine author inputs acquired custody")
			}
		}
	}
	for _, custody := range []*ExecutionEpochConfigCustody{nil, {}} {
		if result, err := custody.Check(t.Context(), 1); err == nil || !reflect.DeepEqual(result, ExecutionEpochConfig{}) {
			t.Fatal("zero owner admitted")
		}
		if custody.ReleaseListener(t.Context(), 1) == nil {
			t.Fatal("zero owner released listener")
		}
		if custody.Close() != nil {
			t.Fatal("zero owner cleanup failed")
		}
	}
}

// This isolates the real existing copy/flag/hash and descriptor lifecycle;
// the partial private author below is only a staging path, never admission.
func TestExecutionEpochConfigProtectedStagingAndClose(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil || os.Chmod(parent, 0o700) != nil {
		t.Fatal("private fixture root unavailable")
	}
	custody := &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{parent: parent}}
	t.Cleanup(func() { _ = custody.Close() })
	var specs []ExecutionInputCopy
	inputs := []epochGeneratedInput{{"catalog-a", []byte("a")}, {"catalog-b", []byte("b")}, {"catalog-a-return", []byte("a-return")}}
	protected, err := custody.protectInputs(t.Context(), inputs)
	if protected != nil {
		for _, input := range inputs {
			specs = append(specs, ExecutionInputCopy{Name: input.name})
		}
		inputCustodyTestCleanup(t, protected, specs)
	}
	if err != nil {
		t.Fatal(err)
	}
	custody.catalogs = protected
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || len(custody.stages) != 0 {
		t.Fatal("temporary input bytes escaped exact cleanup")
	}
	for _, input := range inputs {
		path, err := protected.Check(t.Context(), input.name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, input.raw) {
			t.Fatal("protected bytes differ")
		}
		if writer, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = writer.Close()
			t.Fatal("new copy is writable")
		}
	}
	var addresses []string
	for index := range custody.listeners {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		custody.listeners[index] = listener
		address := listener.Addr().String()
		if slices.Contains(addresses, address) {
			t.Fatal("two reservations share one address")
		}
		addresses = append(addresses, address)
		if competing, err := net.Listen("tcp4", address); err == nil {
			_ = competing.Close()
			t.Fatal("held listener failed to reserve address")
		}
	}
	retained := custody.RetainedPaths()
	if len(retained) != 1 || retained[0] != protected.Directory() {
		t.Fatal("retained ownership omitted protected input")
	}
	for range 2 {
		if custody.Close() != nil {
			t.Fatal("FD-only close failed")
		}
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp4", address)
		if err != nil {
			t.Fatal("close retained its reservation", err)
		}
		_ = listener.Close()
	}
	if _, err := os.Stat(protected.Directory()); err != nil {
		t.Fatal("close deleted retained input")
	}
	if _, err := protected.Check(t.Context(), inputs[0].name); err == nil {
		t.Fatal("closed input owner remained usable")
	}
	if result, err := custody.Check(t.Context(), 1); err == nil || result.ConfigPath != "" {
		t.Fatal("partial test setup became authority")
	}
}
