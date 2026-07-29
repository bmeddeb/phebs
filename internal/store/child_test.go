package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSurrealVersionTokenAcceptsStable3xAndBuildMetadata(t *testing.T) {
	for _, token := range []string{
		"3.0",
		"3.0.0",
		"3.2.3",
		"3.2.3+20260721.40522d1",
		"3.2+local-build",
	} {
		t.Run(token, func(t *testing.T) {
			if err := validateSurrealVersionToken(token); err != nil {
				t.Fatalf("validateSurrealVersionToken(%q): %v", token, err)
			}
		})
	}
}

func TestValidateSurrealVersionTokenRejectsUnsupportedOrInvalid(t *testing.T) {
	for _, token := range []string{
		"",
		" 3.2.3",
		"3",
		"2.9.9",
		"4.0.0",
		"3.2.beta",
		"3.2.3.4",
		"3.2.3-alpha.1",
		"3.2.3+",
		"3.2.3+bad_meta",
	} {
		t.Run(token, func(t *testing.T) {
			if err := validateSurrealVersionToken(token); err == nil {
				t.Fatalf("validateSurrealVersionToken(%q) succeeded", token)
			}
		})
	}
}

func TestInspectSurrealBinaryAcceptsBuildMetadataVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surreal")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '3.2.3+20260721.40522d1 for macos on aarch64'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	identity, err := InspectSurrealBinary(path)
	if err != nil {
		t.Fatalf("InspectSurrealBinary: %v", err)
	}
	if identity.Version != "3.2.3+20260721.40522d1" {
		t.Fatalf("version = %q", identity.Version)
	}
	if identity.Path == "" || !validSHA256(identity.SHA256) {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestLocalRuntimeDescriptorOwnershipAndCleanup(t *testing.T) {
	dataDir := t.TempDir()
	runtime := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	cleanup, err := PublishLocalRuntime(dataDir, runtime)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dataDir, localRuntimeName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime descriptor mode = %v, %v", info, err)
	}
	got, err := ReadLocalRuntime(dataDir)
	if err != nil || got != runtime {
		t.Fatalf("ReadLocalRuntime = %+v, %v", got, err)
	}
	if _, err := PublishLocalRuntime(dataDir, runtime); err == nil || !strings.Contains(err.Error(), "another live server") {
		t.Fatalf("second publisher error = %v", err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(dataDir, localRuntimeName)); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor survived cleanup: %v", err)
	}
}

func TestLocalRuntimeDescriptorRefusesCorruptPredecessor(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, localRuntimeName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	if _, err := PublishLocalRuntime(dataDir, runtime); err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("PublishLocalRuntime error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}\n" {
		t.Fatalf("corrupt predecessor was overwritten: %q, %v", data, err)
	}
}

func TestOpenLocalRefusesCorruptDescriptorBeforeStartingChild(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, localRuntimeName), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLocal(context.Background(), dataDir); err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("OpenLocal error = %v", err)
	}
}

func TestOpenLocalWithConfigRefusesInvalidDigestBeforeStartingChild(t *testing.T) {
	if _, err := OpenLocalWithConfig(context.Background(), t.TempDir(), "bad"); err == nil || !strings.Contains(err.Error(), "config digest") {
		t.Fatalf("OpenLocalWithConfig error = %v", err)
	}
}
