package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t421"
)

func TestAuthorCommandSchemaSelection(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra []string
		want  func(context.Context, string, string, string) (t421.PlanIdentity, error)
	}{
		{"historical_default", nil, t421.Author},
		{"explicit_v2", []string{"-schema", "v2"}, t421.Author},
		{"corrected_v3", []string{"-schema", "v3"}, t421.AuthorV3},
		{"pressure_continuity_v4", []string{"-schema", "v4"}, t421.AuthorV4},
		{"caller_restore_continuity_v5", []string{"-schema", "v5"}, t421.AuthorV5},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"-out", "plan-new.json", "-source-commit", "selected", "-repository-root", "."}, test.extra...)
			options, err := parseAuthorOptions(args, io.Discard)
			root, rootErr := filepath.Abs(".")
			if err != nil || rootErr != nil || options.destination != "plan-new.json" || options.sourceCommit != "selected" || options.repositoryRoot != root ||
				reflect.ValueOf(options.author).Pointer() != reflect.ValueOf(test.want).Pointer() {
				t.Fatal("CLI did not select the exact genuine author without executing it", err)
			}
		})
	}
}

func TestAuthorCommandRefusesBeforeAuthoring(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing_output", []string{"-source-commit", "selected"}},
		{"missing_source", []string{"-out", "plan-new.json"}},
		{"unknown_schema", []string{"-out", "plan-new.json", "-source-commit", "selected", "-schema", "v6"}},
		{"empty_schema", []string{"-out", "plan-new.json", "-source-commit", "selected", "-schema="}},
		{"retired_v1", []string{"-out", "plan-new.json", "-source-commit", "selected", "-schema", "v1"}},
		{"positional", []string{"-out", "plan-new.json", "-source-commit", "selected", "extra"}},
		{"unknown_flag", []string{"-out", "plan-new.json", "-source-commit", "selected", "-unsafe"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseAuthorOptions(test.args, io.Discard)
			if err == nil || options.author != nil {
				t.Fatal("invalid input retained authoring capability")
			}
		})
	}
}

func TestAuthorCommandHelp(t *testing.T) {
	var output bytes.Buffer
	options, err := parseAuthorOptions([]string{"-h"}, &output)
	if !errors.Is(err, flag.ErrHelp) || options.author != nil || !strings.Contains(output.String(), "-schema") || !strings.Contains(output.String(), "-out") {
		t.Fatal("help must retain usage, include schema selection and never author", err, output.String())
	}
}
