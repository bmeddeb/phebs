//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Directory aliases retain a regular final component; sealed-file aliases do not.
// Actual protected-tree ownership is exercised by the Linux epoch-input gate.
func TestT422SemanticLinuxDirectoryFDConfigRoute(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalogs", "catalog-a"), []byte("neutral catalog\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	keeper, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keeper.Close() }()
	route := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), keeper.Fd())
	catalog := route + "/catalogs/catalog-a"
	configRaw := []byte("server: {addr: '127.0.0.1:3070', data_dir: /tmp/t422-data}\nauth: {api_key: exact-test}\nsync: {resync_interval: '0'}\nconnections:\n  - {name: neutral, type: git, url: 'file:///tmp/t422-source.git', watch: true}\nexperimental: {provisional_proto_extraction: true}\nservice_catalogs:\n  local/tmp/t422-source: {kind: operator, id: platform, version: v1, path: " + catalog + ", runtime: v3}\n")
	raw, snapshot := t422SemanticTestRequest(t)
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(configRaw)
	launch.request.ConfigSHA256 = "sha256:" + hex.EncodeToString(digest[:])
	configPath := filepath.Join(root, "config")
	if err := os.WriteFile(configPath, configRaw, 0o400); err != nil {
		t.Fatal(err)
	}
	cfg, bound, err := launch.loadConfig(route+"/config", false)
	if err != nil || cfg == nil || !bytes.Equal(bound, configRaw) || cfg.ServiceCatalogs[launch.request.Repository].Path != catalog {
		t.Fatal("regular final component or embedded catalog route changed", err)
	}
	link := filepath.Join(root, "config-link")
	if err := os.Symlink(configPath, link); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.MemfdCreate("t422-config-route", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Fatal(err)
	}
	sealed := os.NewFile(uintptr(fd), "sealed-config-route")
	defer func() { _ = sealed.Close() }()
	if n, err := sealed.Write(configRaw); err != nil || n != len(configRaw) {
		t.Fatal("sealed fixture write", err)
	}
	if err := sealed.Chmod(0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(sealed.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{link, fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), sealed.Fd())} {
		if _, _, err := launch.loadConfig(selected, false); !errors.Is(err, errT422SemanticLaunch) {
			t.Fatal("final symlink alias escaped the regular-file predicate", err)
		}
	}
}
