package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalRepoNameMatchesGenericGitShape(t *testing.T) {
	root := t.TempDir()
	name, err := localRepoName(filepath.Join(root, "fixture.git"))
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(filepath.Join(root, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	want := "local/" + strings.Trim(filepath.ToSlash(absolute), "/")
	if name != want {
		t.Fatalf("localRepoName() = %q, want %q", name, want)
	}
}

func TestExactServerEnvRemovesFixtureAndChildOverrides(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"PHEBS_ZOEKT_GIT_INDEX=/workspace/indexer",
		"PHEBS_BUF=/workspace/buf",
		"PHEBS_SURREAL=/workspace/surreal",
		"PHEBS_INVESTIGATION_FIXTURES=/workspace/investigations",
		"PHEBS_CONTRACT_ATLAS_FIXTURE=/workspace/contracts.json",
		"SAFE=value",
	}
	got := exactServerEnv(environ, "/bundle", "/prereq/surreal")
	want := []string{
		"PATH=/usr/bin",
		"SAFE=value",
		"PHEBS_ZOEKT_GIT_INDEX=/bundle/bin/zoekt-git-index",
		"PHEBS_BUF=/bundle/bin/buf",
		"PHEBS_SURREAL=/prereq/surreal",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exactServerEnv() = %#v, want %#v", got, want)
	}
}

func TestWriteConfigHasDarkExperimentalPosture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeConfig(path, "/data", "127.0.0.1:1234", "/fixture"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"data_dir": "/data"`,
		`"cookie_secure": false`,
		`"url": "/fixture"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config missing %s:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"experimental", "contract_atlas", "fixture"} {
		if forbidden != "fixture" && strings.Contains(text, forbidden) {
			t.Errorf("config contains dark-only key %q:\n%s", forbidden, text)
		}
	}
}
