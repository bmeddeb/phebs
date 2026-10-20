//go:build darwin || linux

package t421

import (
	"bytes"
	"encoding/json"
	"reflect"
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
		raw, observed, err := epochConfigBytesParsed(plan, epoch, source)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := config.ParseLiteral(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(parsed, observed) {
			t.Fatal("retained config facts differ from parsing the actual bytes")
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
	for _, mode := range []string{"runtime", "catalog-version", "catalog-path", "poll", "watch", "details", "proto", "thrift", "thrift-field", "kafka", "lifecycle", "permissions", "revisions", "analysis-unit", "contexts", "webhook", "audit", "cookie", "session", "source", "api-key", "address", "ambient-secret", "unknown", "bytes"} {
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
			if observed, err := parseEpochConfigBytes(plan, epoch, source, changed); err == nil || observed != nil {
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

func TestExecutionEpochConfigRefusesManagedInstallation(t *testing.T) {
	plan := accountingTestPlan(t)
	epoch, source := epochTestSelection(t, 0)
	raw, err := epochConfigBytes(plan, epoch, source)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("managed_scip:\n  manifest: /private/fixture/installation.json\n  sha256: sha256:"+strings.Repeat("a", 64)+"\n")...)
	if cfg, err := config.ParseLiteral(raw); err != nil || cfg.ManagedSCIP == nil {
		t.Fatal("managed installation fixture must be valid ordinary configuration", err)
	}
	if cfg, err := parseEpochConfigBytes(plan, epoch, source, raw); err == nil || cfg != nil {
		t.Fatal("unaccounted managed installation admitted by epoch projection")
	}
}

func epochConfigBytes(plan Plan, epoch ExecutionEpochConfig, source string) ([]byte, error) {
	raw, _, err := epochConfigBytesParsed(plan, epoch, source)
	return raw, err
}

func validateEpochConfigBytes(plan Plan, epoch ExecutionEpochConfig, source string, raw []byte) error {
	_, err := parseEpochConfigBytes(plan, epoch, source, raw)
	return err
}
