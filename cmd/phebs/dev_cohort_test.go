package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func TestDevCohortSelection(t *testing.T) {
	for _, row := range []struct {
		name, value string
		present     bool
		want        devCohort
	}{
		{name: "absent preserves explicit legacy fixture", want: devCohortFocused},
		{name: "make default whole", value: "whole", present: true, want: devCohortWhole},
		{name: "explicit focused", value: "focused", present: true, want: devCohortFocused},
		{name: "explicit empty refuses", present: true},
		{name: "unknown refuses", value: "automatic", present: true},
		{name: "padded refuses", value: "whole ", present: true},
		{name: "case refuses", value: "Whole", present: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := parseDevCohort(row.value, row.present)
			if row.want == "" {
				if err == nil {
					t.Fatal("invalid selector admitted")
				}
				return
			}
			if err != nil || got != row.want {
				t.Fatalf("selection = %q, %v; want %q", got, err, row.want)
			}
		})
	}
}

func TestDevCohortWholeAndFocusedBindings(t *testing.T) {
	fixture, catalog := t307FixturePath(t), t335CatalogPath(t)
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []devCohort{devCohortWhole, devCohortFocused} {
		t.Run(string(cohort), func(t *testing.T) {
			cfg := &config.Config{}
			for range 2 {
				if err := bindT307NeutralServiceDemo(cfg, fixture, cohort); err != nil {
					t.Fatal(err)
				}
				if err := bindT335ServiceDirectoryDemo(cfg, fixture, catalog, cohort); err != nil {
					t.Fatal(err)
				}
				if err := bindT344ServiceSearchDemo(cfg, t344FixturePath(t), t344CatalogPath(t)); err != nil {
					t.Fatal(err)
				}
			}
			if len(cfg.Connections) != 2 || len(cfg.ServiceCatalogs) != 2 {
				t.Fatalf("repeat selection duplicated inputs: %+v", cfg)
			}
			if cfg.Experimental != (config.Experimental{ProvisionalProtoExtraction: true, ProvisionalKafkaExtraction: true}) {
				t.Fatalf("demo widened evidence flags: %+v", cfg.Experimental)
			}
			unit, focused := cfg.AnalysisUnits[repository]
			if focused != (cohort == devCohortFocused) || focused && !reflect.DeepEqual(unit, t307DemoAnalysisUnit()) {
				t.Fatalf("wrong %s unit: %+v", cohort, cfg.AnalysisUnits)
			}
			if len(cfg.AnalysisUnits) != map[devCohort]int{devCohortWhole: 0, devCohortFocused: 1}[cohort] {
				t.Fatalf("unexpected additional units: %+v", cfg.AnalysisUnits)
			}
			if got := cfg.ServiceCatalogs[repository]; got != (config.ServiceCatalog{
				Kind: servicecatalog.AuthorityOperator, ID: "t335-demo", Version: "v1", Path: catalog,
			}) {
				t.Fatalf("catalog selection widened: %+v", got)
			}
		})
	}
}

func TestDevCohortRefusesConflictingScopeBeforeMutation(t *testing.T) {
	fixture, catalog := t307FixturePath(t), t335CatalogPath(t)
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		t.Fatal(err)
	}
	exactUnit := t307DemoAnalysisUnit()
	for _, row := range []struct {
		name       string
		cohort     devCohort
		unit       *analysisunit.Config
		connection bool
		catalog    bool
	}{
		{name: "whole refuses exact focused unit", cohort: devCohortWhole, unit: &exactUnit},
		{name: "whole refuses unrelated unit", cohort: devCohortWhole, unit: &analysisunit.Config{Name: "other", Primary: []string{"other"}}},
		{name: "whole catalog refuses focused binding", cohort: devCohortWhole, unit: &exactUnit, connection: true, catalog: true},
		{name: "focused catalog requires unit", cohort: devCohortFocused, connection: true, catalog: true},
		{name: "focused catalog refuses name-only scope", cohort: devCohortFocused, unit: &analysisunit.Config{Name: "orders-service", Primary: []string{"other"}}, connection: true, catalog: true},
		{name: "whole catalog requires connection", cohort: devCohortWhole, catalog: true},
		{name: "invalid binder mode", cohort: "", connection: true},
		{name: "invalid catalog mode", cohort: "", connection: true, catalog: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg := &config.Config{}
			if row.unit != nil {
				cfg.AnalysisUnits = map[string]analysisunit.Config{repository: *row.unit}
			}
			if row.connection {
				cfg.Connections = []config.Connection{{Name: "existing", Type: "git", URL: fixture}}
			}
			before := cloneT307Config(cfg)
			var err error
			if row.catalog {
				err = bindT335ServiceDirectoryDemo(cfg, fixture, catalog, row.cohort)
			} else {
				err = bindT307NeutralServiceDemo(cfg, fixture, row.cohort)
			}
			if err == nil || !reflect.DeepEqual(*cfg, before) {
				t.Fatalf("invalid binding changed or admitted configuration: %v, %+v", err, cfg)
			}
		})
	}
}

func TestLoadServeDevCohort(t *testing.T) {
	for _, row := range []struct {
		name, selector string
		present, demo  bool
		wantFocused    bool
		wantError      bool
	}{
		{name: "ordinary serve remains unchanged"},
		{name: "absent selector keeps legacy", demo: true, wantFocused: true},
		{name: "make selects whole", selector: "whole", present: true, demo: true},
		{name: "explicit focused", selector: "focused", present: true, demo: true, wantFocused: true},
		{name: "explicit empty", present: true, demo: true, wantError: true},
		{name: "unknown", selector: "auto", present: true, demo: true, wantError: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			for _, key := range []string{"PHEBS_T307_NEUTRAL_SERVICE_REPO", "PHEBS_T335_SERVICE_CATALOG", "PHEBS_T344_SERVICE_SEARCH_REPO", "PHEBS_T344_SERVICE_SEARCH_CATALOG", "PHEBS_THRIFT_FIELD_DEMO_REPO", "PHEBS_DEV_COHORT"} {
				t.Setenv(key, "")
			}
			if row.present {
				t.Setenv("PHEBS_DEV_COHORT", row.selector)
			} else if err := os.Unsetenv("PHEBS_DEV_COHORT"); err != nil {
				t.Fatal(err)
			}
			if row.demo {
				t.Setenv("PHEBS_T307_NEUTRAL_SERVICE_REPO", t307FixturePath(t))
				t.Setenv("PHEBS_T335_SERVICE_CATALOG", t335CatalogPath(t))
				t.Setenv("PHEBS_T344_SERVICE_SEARCH_REPO", t344FixturePath(t))
				t.Setenv("PHEBS_T344_SERVICE_SEARCH_CATALOG", t344CatalogPath(t))
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("server:\n  addr: 127.0.0.1:0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := loadServeConfig(nil, &serveFlags{configPath: path})
			if row.wantError {
				if err == nil || cfg != nil {
					t.Fatal("invalid selector admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantConnections, wantUnits := 0, 0
			if row.demo {
				wantConnections = 2
			}
			if row.wantFocused {
				wantUnits = 1
			}
			if len(cfg.Connections) != wantConnections || len(cfg.ServiceCatalogs) != wantConnections || len(cfg.AnalysisUnits) != wantUnits {
				t.Fatalf("load crossed cohort boundary: %+v", cfg)
			}
		})
	}
}

func TestDevCohortWholeCatalogGuards(t *testing.T) {
	fixture, catalog := t307FixturePath(t), t335CatalogPath(t)
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	drifted := filepath.Join(t.TempDir(), "t335-service-catalog.json")
	if err := os.WriteFile(drifted, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name, path string
		conflict   bool
	}{
		{name: "retained bytes required", path: drifted},
		{name: "existing catalog never replaced", path: catalog, conflict: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg := &config.Config{}
			if err := bindT307NeutralServiceDemo(cfg, fixture, devCohortWhole); err != nil {
				t.Fatal(err)
			}
			if row.conflict {
				cfg.ServiceCatalogs = map[string]config.ServiceCatalog{repository: {
					Kind: servicecatalog.AuthorityOperator, ID: "other", Version: "v1", Path: catalog,
				}}
			}
			before := cloneT307Config(cfg)
			if err := bindT335ServiceDirectoryDemo(cfg, fixture, row.path, devCohortWhole); err == nil || !reflect.DeepEqual(*cfg, before) {
				t.Fatalf("catalog guard admitted or changed config: %v, %+v", err, cfg)
			}
		})
	}
}

func TestDevCohortMakeRecipes(t *testing.T) {
	for _, target := range []string{"dev", "dev-api"} {
		for _, row := range []struct {
			name, value string
			explicit    bool
			want        string
		}{
			{name: "default whole", want: "whole"},
			{name: "focused opt-in", value: "focused", explicit: true, want: "focused"},
			{name: "empty refused", explicit: true},
			{name: "unknown refused", value: "auto", explicit: true},
		} {
			t.Run(target+"/"+row.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				args := []string{"--no-print-directory", "-n", "-o", "bin/zoekt-git-index", "-o", "bin/phebs-focused-index", "-o", "bin/buf", "-o", "ui", target,
					"TARGET_GOOS=darwin", "TARGET_GOARCH=arm64", "RELEASE_COMMIT=dry-run"}
				if row.explicit {
					args = append(args, "PHEBS_DEV_COHORT="+row.value)
				}
				cmd := exec.CommandContext(ctx, "make", args...)
				cmd.Dir = filepath.Clean("../..")
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "PHEBS_DEV_COHORT=") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				out, err := cmd.CombinedOutput()
				if row.want == "" {
					if err == nil || !strings.Contains(string(out), "PHEBS_DEV_COHORT must be exactly whole or focused") {
						t.Fatalf("invalid selector did not refuse before recipes: %v, %s", err, out)
					}
					return
				}
				if err != nil || !strings.Contains(string(out), "PHEBS_DEV_COHORT='"+row.want+"'") {
					t.Fatalf("wrong recipe selection: %v, %s", err, out)
				}
			})
		}
	}
}
