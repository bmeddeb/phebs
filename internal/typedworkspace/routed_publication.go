package typedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// RoutedAuthority is trusted current-store custody, never request input. The
// controller obtains these scalars from the completed attempt/current envelope.
type RoutedAuthority struct {
	Owner                           OwnerIdentity
	ManifestDigest                  string
	DirectoryDevice, DirectoryInode uint64
	PublicationReceiptDigest        string
	Parent, Execution               typedindex.Admission
	RootDigest                      string
}

func (a RoutedAuthority) valid() bool {
	return a.Owner.valid() && a.Owner.PlanningDigest == a.Parent.Digest() && a.Owner.Request == a.Parent.Request() && publicationAuthority(a.Parent, a.Execution, a.Execution.Request().PlanDigest) && publicationHash(a.RootDigest) && publicationHash(a.ManifestDigest) && publicationHash(a.PublicationReceiptDigest) && a.DirectoryInode != 0
}

// Identity includes custody as well as semantic root, preventing lease/ABA reuse.
func (a RoutedAuthority) Identity() string {
	if !a.valid() {
		return ""
	}
	b, _ := json.Marshal(struct {
		Owner                                      OwnerIdentity
		Manifest, Receipt, Parent, Execution, Root string
		Device, Inode                              uint64
	}{a.Owner, a.ManifestDigest, a.PublicationReceiptDigest, a.Parent.Digest(), a.Execution.Digest(), a.RootDigest, a.DirectoryDevice, a.DirectoryInode})
	return publicationDigest(b)
}

// RoutedMetadata is immutable, pin-free cached metadata. Accessors expose only
// immutable routing or scalar copies. No idle cache retains a physical handle.
type RoutedMetadata struct {
	authority string
	base      string
	receipt   PublicationReceipt
	files     map[string]typedindex.BundleFile
	nodes     map[string]Node
	routing   typedindex.Routing
	bytes     int64
}

func (m *RoutedMetadata) AccountedBytes() int64 {
	if m == nil {
		return 0
	}
	return m.bytes
}
func (m *RoutedMetadata) Routing() typedindex.Routing {
	if m == nil {
		return typedindex.Routing{}
	}
	return m.routing
}

type RoutedPublication struct {
	publication *Publication
	metadata    *RoutedMetadata
}

func (p *RoutedPublication) Metadata() *RoutedMetadata { return p.metadata }
func (p *RoutedPublication) Close() error {
	if p == nil {
		return nil
	}
	return p.publication.Close()
}

// OpenRoutedPublication acquires base then attempt pins before checking owner
// custody. Cold opens read only receipt/root/attempt/document/symbol controls.
// Warm opens reuse authenticated metadata and read only the small owner control;
// they check the exact publication root inode. Neither path scans SCIP members.
// Caller retains this handle through source reads and final current revalidation.
func OpenRoutedPublication(ctx context.Context, base string, a RoutedAuthority, cached *RoutedMetadata) (_ *RoutedPublication, err error) {
	if ctx == nil || !a.valid() {
		return nil, ErrCustody
	}
	baseRelease, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if baseRelease != nil {
			baseRelease()
		}
	}()
	parent := filepath.Join(base, a.Owner.RelativeName())
	release, err := publicationLease(ctx, parent, false, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	m, err := loadOwner(ctx, base, a.Owner, false)
	if err != nil || m.Digest() != a.ManifestDigest || m.Revision != 3 || m.Directory.Device != a.DirectoryDevice || m.Directory.Inode != a.DirectoryInode || m.Publication == nil || m.Publication.Digest != a.PublicationReceiptDigest {
		return nil, ErrCustody
	}
	// The child pin now protects this attempt and its ancestors. Retirement
	// cannot rename an ancestor without this exclusive child guard; request
	// directories are removed only when empty. Do not hold the global namespace
	// across receipt/control decoding or source reads.
	baseRelease()
	baseRelease = nil
	meta := cached
	if meta != nil && (meta.authority != a.Identity() || meta.base != base) {
		return nil, ErrCustody
	}
	if meta == nil {
		dir, e := openDirectory(parent, true)
		if e != nil {
			return nil, e
		}
		raw, _, readErr := ownerReadControl(ctx, dir, "publication-receipt.json", MaxPublicationReceiptBytes, m.Publication)
		if e = errors.Join(readErr, dir.Close()); e != nil {
			return nil, e
		}
		receipt, e := DecodePublicationReceipt(raw)
		if e != nil {
			return nil, e
		}
		tree, e := validatePublicationReceipt(receipt)
		if e != nil || len(receipt.Nodes) != len(tree.entries) || receipt.Name != m.PublicationName || receipt.ParentDigest != a.Parent.Digest() || receipt.RequestDigest != a.Execution.Digest() || receipt.PlanDigest != a.Execution.Request().PlanDigest || receipt.RootDigest != a.RootDigest {
			return nil, ErrCustody
		}
		meta = &RoutedMetadata{authority: a.Identity(), base: base, receipt: receipt, files: map[string]typedindex.BundleFile{}, nodes: map[string]Node{}, bytes: 512 + int64(len(raw))*2}
		for _, f := range receipt.Files {
			meta.files[f.Path] = f
			meta.bytes += 192 + int64(len(f.Path)+len(f.Digest))
		}
		for _, n := range receipt.Nodes {
			meta.nodes[n.Path] = n
			meta.bytes += 128 + int64(len(n.Path))
		}
	}
	dir, err := openDirectory(parent, true)
	if err != nil {
		return nil, err
	}
	root, openErr := openRelative(dir, meta.receipt.Name, true)
	err = errors.Join(openErr, dir.Close())
	if err != nil {
		if root != nil {
			_ = root.Close()
		}
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = root.Close()
		}
	}()
	node, err := directoryInfo(root, ".", true)
	if err != nil || node != meta.nodes["."] {
		return nil, ErrCustody
	}
	p := &Publication{root: root, release: release, files: meta.files, nodes: meta.nodes}
	if cached == nil {
		read := func(name string) ([]byte, error) {
			row, ok := meta.files[name]
			if !ok {
				return nil, ErrCustody
			}
			return readRoutedFile(ctx, root, row, meta.nodes)
		}
		raw, e := read("root.json")
		if e != nil {
			return nil, e
		}
		control, e := typedindex.DecodeRoutingRoot(ctx, a.Execution, a.RootDigest, raw)
		if e != nil {
			return nil, e
		}
		attempt, e := read(control.Attempt.Name)
		if e != nil {
			return nil, e
		}
		documents, e := read(control.Documents.Name)
		if e != nil {
			return nil, e
		}
		symbols, e := read(control.Symbols.Name)
		if e != nil {
			return nil, e
		}
		routing, e := typedindex.DecodeRouting(ctx, a.Execution, a.RootDigest, raw, attempt, documents, symbols)
		if e != nil {
			return nil, e
		}
		refs := routing.Files()
		if len(refs)+4 != len(meta.files) {
			return nil, ErrCustody
		}
		for _, ref := range refs {
			f, ok := meta.files[ref.Name]
			if !ok || f.Bytes != int64(ref.Bytes) || f.Digest != ref.Digest {
				return nil, ErrCustody
			}
		}
		for name, want := range map[string]string{"parent.json": a.Parent.Digest(), "request.json": a.Execution.Digest(), "plan.json": a.Execution.Request().PlanDigest, "root.json": a.RootDigest} {
			if meta.files[name].Digest != want {
				return nil, ErrCustody
			}
		}
		meta.routing = routing
		meta.bytes += routing.AccountedBytes()
	}
	p.control = meta.routing.Root()
	return &RoutedPublication{p, meta}, nil
}

// Check each selected path's declared ancestor inode/mode, then use the existing
// descriptor-bound file identity/hash reader. This is O(path depth), not a census.
func readRoutedFile(ctx context.Context, root *os.File, row typedindex.BundleFile, nodes map[string]Node) ([]byte, error) {
	for name := path.Dir(row.Path); name != "."; name = path.Dir(name) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, e := openRelative(root, name, true)
		if e != nil {
			return nil, e
		}
		node, e := directoryInfo(f, name, true)
		e = errors.Join(e, f.Close())
		if e != nil || node != nodes[name] {
			return nil, ErrCustody
		}
	}
	return readPublicationFile(ctx, root, row, nodes[row.Path])
}
func (p *RoutedPublication) read(ctx context.Context, name string) ([]byte, error) {
	if p == nil || ctx == nil {
		return nil, ErrCustody
	}
	p.publication.mu.Lock()
	defer p.publication.mu.Unlock()
	row, ok := p.metadata.files[name]
	if p.publication.root == nil || !ok {
		return nil, ErrCustody
	}
	return readRoutedFile(ctx, p.publication.root, row, p.metadata.nodes)
}
func (p *RoutedPublication) ReadMember(ctx context.Context, name string) ([]byte, error) {
	if p == nil || !strings.HasPrefix(name, "members/") {
		return nil, ErrCustody
	}
	raw, err := p.read(ctx, name)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
func (p *RoutedPublication) ReadGenerated(ctx context.Context, name string) ([]byte, error) {
	if p == nil {
		return nil, ErrCustody
	}
	d, ok := p.metadata.routing.Generated(name)
	if !ok {
		return nil, ErrCustody
	}
	raw, err := p.read(ctx, name)
	if err != nil {
		return nil, err
	}
	if err = typedindex.VerifyGeneratedDocument(ctx, d, p.metadata.routing.Root().Binding, d.Unit, raw); err != nil {
		return nil, err
	}
	return raw, nil
}
