//go:build linux

package typedworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func routedPublicationFixture(t *testing.T) (string, RoutedAuthority, PublicationReceipt) {
	t.Helper()
	f, src, raw, inv, id, m := ownerFixture(t)
	base := f.dir
	attempt := filepath.Join(base, id.RelativeName())
	input, e := Copy(t.Context(), src, attempt, inv, f.gate)
	if e != nil {
		t.Fatal(e)
	}
	m, e = SaveOwnerInputs(t.Context(), base, id, m.Digest(), raw, input, f.gate)
	if e != nil {
		t.Fatal(e)
	}
	f.dir = attempt
	pub := f.install(t)
	m, e = SaveOwnerPublication(t.Context(), base, id, m.Digest(), f.parent, f.execution, pub, f.gate)
	if e != nil {
		t.Fatal(e)
	}
	return base, RoutedAuthority{Owner: id, ManifestDigest: m.Digest(), DirectoryDevice: m.Directory.Device, DirectoryInode: m.Directory.Inode, PublicationReceiptDigest: m.Publication.Digest, Parent: f.parent, Execution: f.execution, RootDigest: f.bundle.RootDigest()}, pub
}
func TestRoutedPublicationColdWarmAndPins(t *testing.T) {
	base, a, receipt := routedPublicationFixture(t)
	p, e := OpenRoutedPublication(t.Context(), base, a, nil)
	if e != nil {
		t.Fatal(e)
	}
	meta := p.Metadata()
	if meta.AccountedBytes() <= 0 || meta.Routing().Digest() != a.RootDigest {
		t.Fatal("metadata")
	}
	route, ok := meta.Routing().Document("a.go")
	if !ok {
		t.Fatal("route")
	}
	if _, e = p.ReadMember(t.Context(), route.Member); e != nil {
		t.Fatal(e)
	}
	// The base namespace remains available; the exact attempt cannot retire.
	release, e := AcquirePublicationMutation(t.Context(), base)
	if e != nil {
		t.Fatal(e)
	}
	release()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	release, e = AcquirePublicationMutation(ctx, filepath.Join(base, a.Owner.RelativeName()))
	cancel()
	if e == nil {
		release()
		t.Fatal("unpinned attempt")
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	release, e = AcquirePublicationMutation(t.Context(), filepath.Join(base, a.Owner.RelativeName()))
	if e != nil {
		t.Fatal(e)
	}
	release()
	// Same-inode corrupt control bytes are not reread by the authenticated warm
	// metadata path. A cold reader still detects them. No idle FD/pin remains.
	rootFile := filepath.Join(base, a.Owner.RelativeName(), receipt.Name, "root.json")
	raw, e := os.ReadFile(rootFile)
	if e != nil {
		t.Fatal(e)
	}
	raw[0] ^= 1
	if e = os.Chmod(rootFile, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(rootFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(rootFile, 0444); e != nil {
		t.Fatal(e)
	}
	warm, e := OpenRoutedPublication(t.Context(), base, a, meta)
	if e != nil {
		t.Fatal("warm reread controls", e)
	}
	if warm.Metadata() != meta {
		t.Fatal("metadata replaced")
	}
	_ = warm.Close()
	if cold, e := OpenRoutedPublication(t.Context(), base, a, nil); e == nil {
		_ = cold.Close()
		t.Fatal("cold ignored control corruption")
	}
}
func TestRoutedPublicationSelectedReadAndAuthority(t *testing.T) {
	base, a, receipt := routedPublicationFixture(t)
	p, e := OpenRoutedPublication(t.Context(), base, a, nil)
	if e != nil {
		t.Fatal(e)
	}
	meta := p.Metadata()
	route, _ := meta.Routing().Document("a.go")
	_ = p.Close()
	member := filepath.Join(base, a.Owner.RelativeName(), receipt.Name, route.Member)
	raw, e := os.ReadFile(member)
	if e != nil {
		t.Fatal(e)
	}
	raw[len(raw)-1] ^= 1
	if e = os.Chmod(member, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(member, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(member, 0444); e != nil {
		t.Fatal(e)
	}
	// Cold opening controls never scans a SCIP member.
	p, e = OpenRoutedPublication(t.Context(), base, a, nil)
	if e != nil {
		t.Fatal("eager member read", e)
	}
	if _, e = p.ReadMember(t.Context(), route.Member); e == nil {
		t.Fatal("changed member")
	}
	_ = p.Close()
	for _, kind := range []string{"owner", "directory", "receipt", "root", "cached"} {
		t.Run(kind, func(t *testing.T) {
			bad := a
			switch kind {
			case "owner":
				bad.ManifestDigest = digest([]byte("other"))
			case "directory":
				bad.DirectoryInode++
			case "receipt":
				bad.PublicationReceiptDigest = digest([]byte("other"))
			case "root":
				bad.RootDigest = digest([]byte("other"))
			case "cached":
				bad.Owner.LeaseDigest = digest([]byte("other"))
			}
			got, e := OpenRoutedPublication(t.Context(), base, bad, meta)
			if e == nil {
				_ = got.Close()
				t.Fatal("wrong authority")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, e := OpenRoutedPublication(ctx, base, a, meta); e == nil {
		_ = got.Close()
		t.Fatal("canceled open")
	}
}
