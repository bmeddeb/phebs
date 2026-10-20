//go:build linux

package servicecatalogingest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"golang.org/x/sys/unix"
)

func TestSelectedCatalogLinuxDirectoryFDRoute(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "catalog-a")
	writeCatalog(t, path, testCatalog("0123456789012345678901234567890123456789", "Orders"))
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keeper, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keeper.Close() }()
	route := fmt.Sprintf("/proc/%d/fd/%d/catalog-a", os.Getpid(), keeper.Fd())
	got, err := readSelectedFile(route, servicecatalog.MaxEncodedBytes)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("directory-FD route lost exact regular catalog", err)
	}
	link := filepath.Join(root, "catalog-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.MemfdCreate("selected-catalog-route", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Fatal(err)
	}
	sealed := os.NewFile(uintptr(fd), "sealed-catalog-route")
	defer func() { _ = sealed.Close() }()
	if n, err := sealed.Write(want); err != nil || n != len(want) {
		t.Fatal("sealed fixture write", err)
	}
	if err := sealed.Chmod(0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(sealed.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{link, fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), sealed.Fd())} {
		if _, err := readSelectedFile(selected, servicecatalog.MaxEncodedBytes); err == nil {
			t.Fatal("final symlink alias escaped selected-catalog refusal")
		}
	}
}
