package t4013

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedExactRegularRefusesUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tests := []struct {
		name    string
		path    func() string
		maximum int
		wantErr bool
	}{
		{name: "valid maximum", path: func() string {
			return write("maximum", bytes.Repeat([]byte{'x'}, 32))
		}, maximum: 32},
		{name: "maximum plus one", path: func() string {
			return write("oversized", bytes.Repeat([]byte{'x'}, 33))
		}, maximum: 32, wantErr: true},
		{name: "symlink", path: func() string {
			target := write("target", []byte("control\n"))
			path := filepath.Join(root, "link")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			return path
		}, maximum: 32, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := readAtomicRegular(test.path(), test.maximum)
			if (err != nil) != test.wantErr {
				t.Fatalf("read = %d bytes, %v", len(raw), err)
			}
		})
	}
}

func TestBoundedExactRegularRefusesReplacementDuringRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openNoFollowRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!\n"), 0o600); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := readOpenedAtomicRegular(path, 32, opened, file); err == nil {
		t.Fatal("replacement retained exact-control authority")
	}
}

func TestBoundedExactRegularDoesNotClassifyPostOpenRemovalAsAbsence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control")
	if _, err := readAtomicRegular(path, 32); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-open absence error = %v, want absent sentinel", err)
	}
	if err := os.WriteFile(path, []byte("control\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openNoFollowRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := readOpenedAtomicRegular(path, 32, opened, file); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-open removal error = %v, want unstable authority without absent sentinel", err)
	}
}

func TestExactControlCanonicalAndHistoricalBoundaries(t *testing.T) {
	root := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	envelope := freezeEnvelope{
		Schema: "t4013-freeze-envelope-v1", CeremonyID: "neutral-35",
		SourceCommit: strings.Repeat("a", 40), PlanDigest: "sha256:" + strings.Repeat("b", 64),
		SignerFingerprint: "SHA256:test", FrozenAt: "2026-08-23T00:00:00Z",
	}
	canonical, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	if value, err := InspectCeremonyJSONValue(write("freeze.json", canonical), "source_commit"); err != nil || value != envelope.SourceCommit {
		t.Fatalf("canonical value = %q, %v", value, err)
	}
	if _, err := InspectCeremonyJSONValue(write("trailing.json", append(canonical, []byte("{}\n")...)), "source_commit"); err == nil {
		t.Fatal("trailing JSON retained ceremony-control authority")
	}

	historical, err := frozenV24PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	historicalRaw, err := MarshalPlan(historical)
	if err != nil {
		t.Fatal(err)
	}
	historicalRaw = bytes.Replace(historicalRaw, []byte("{\n"), []byte("{ \n"), 1)
	if schema, _, err := InspectPlanControl(write("v24.json", historicalRaw)); err != nil || schema != PlanSchemaV24 {
		t.Fatalf("retained V24 plan = %q, %v", schema, err)
	}

	_, v25Raw := v25TestPlan(t)
	v25Raw = bytes.Replace(v25Raw, []byte("{\n"), []byte("{ \n"), 1)
	if _, _, err := InspectPlanControl(write("v25.json", v25Raw)); err == nil {
		t.Fatal("noncanonical V25 plan retained authority")
	}
}

func TestBoundedExactDirectoryRefusesMaximumPlusOne(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := InspectExactDirectory(root, []string{"a"}); err == nil {
		t.Fatal("maximum-plus-one directory entry passed")
	}
	if err := os.Remove(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if err := InspectExactDirectory(root, []string{"a"}); err != nil {
		t.Fatalf("valid maximum directory failed: %v", err)
	}
	empty := t.TempDir()
	if err := InspectExactDirectory(empty, nil); err != nil {
		t.Fatalf("empty exact directory failed: %v", err)
	}
}
