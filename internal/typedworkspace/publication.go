package typedworkspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const publicationSchema = "phebs-typed-publication-custody-v1"
const publicationLock = ".phebs-index-publication.lock"
const maxPublicationFiles = typedindex.MaxSCIPMembers + typedindex.MaxBundleDocuments + 7
const maxInstalledPublicationBytes = typedindex.MaxPublicationBytes + typedindex.MaxPlanBytes + 2*typedindex.MaxRequestBytes

// Derived from existing entry/path ceilings, including worst-case JSON escaping.
const MaxPublicationReceiptBytes = (typedindex.MaxInventoryDirectories + maxPublicationFiles + 1) * (6*512 + 256)

// PublicationReceipt is trusted service custody, never client input or pointer
// authority. Persist it durably outside the publication before committing a
// current pointer. On failure Name identifies residue, not a usable publication.
type PublicationReceipt struct {
	Schema        string                  `json:"schema"`
	Name          string                  `json:"name"`
	ParentDigest  string                  `json:"parent_digest"`
	RequestDigest string                  `json:"request_digest"`
	PlanDigest    string                  `json:"plan_digest"`
	RootDigest    string                  `json:"root_digest"`
	Files         []typedindex.BundleFile `json:"files"`
	Nodes         []Node                  `json:"nodes"`
}

// Publication holds one shared kernel lease until Close. Callers bound the
// number of open publications; one handle serializes its member reads and Close.
// Lifecycle must hold AcquirePublicationMutation before renaming or unlinking.
type Publication struct {
	mu      sync.Mutex
	root    *os.File
	release func()
	control typedindex.BundleRoot
	files   map[string]typedindex.BundleFile
	nodes   map[string]Node
}

func publicationDigest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func publicationHash(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") {
		return false
	}
	b, err := hex.DecodeString(s[7:])
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}
func publicationName(s string) bool {
	return strings.HasPrefix(s, "bundle-") && publishedName("inputs-"+strings.TrimPrefix(s, "bundle-"))
}

// EncodePublicationReceipt bounds all dimensions before marshaling. Partial
// stage receipts remain serializable for recovery, but Open never admits them.
func EncodePublicationReceipt(r PublicationReceipt) ([]byte, error) {
	if _, err := validatePublicationReceipt(r); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxPublicationReceiptBytes {
		return nil, ErrCustody
	}
	return raw, nil
}

func validatePublicationReceipt(r PublicationReceipt) (layout, error) {
	if r.Schema != publicationSchema || !publicationName(strings.TrimSuffix(r.Name, ".stage")) || !publicationHash(r.ParentDigest) || !publicationHash(r.RequestDigest) || !publicationHash(r.PlanDigest) || !publicationHash(r.RootDigest) {
		return layout{}, ErrCustody
	}
	tree, err := publicationLayout(r.Files)
	if err != nil || len(r.Nodes) != 0 && len(r.Nodes) != len(tree.entries) {
		return layout{}, ErrCustody
	}
	previous := ""
	for _, n := range r.Nodes {
		directory, ok := tree.entries[n.Path]
		if !ok || n.Path <= previous || n.Directory != directory || n.Inode == 0 {
			return layout{}, ErrCustody
		}
		previous = n.Path
	}
	return tree, nil
}

func DecodePublicationReceipt(raw []byte) (PublicationReceipt, error) {
	var r PublicationReceipt
	if len(raw) == 0 || len(raw) > MaxPublicationReceiptBytes || !publicationReceiptDimensions(raw) {
		return r, ErrCustody
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return PublicationReceipt{}, ErrCustody
	}
	canonical, err := EncodePublicationReceipt(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return PublicationReceipt{}, ErrCustody
	}
	return r, nil
}

// Check the closed receipt's array lengths before the typed decoder allocates
// slices. Token traversal retains no JSON tree, but a single decoded scalar may
// reach the raw receipt byte ceiling before its 512-byte semantic check refuses.
func publicationReceiptDimensions(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	read := func(want json.Token) bool { got, err := d.Token(); return err == nil && got == want }
	scalar := func() bool {
		value, err := d.Token()
		if err != nil {
			return false
		}
		switch value := value.(type) {
		case string:
			return len(value) <= 512
		case json.Number:
			return len(value) <= 20
		case bool:
			return true
		default:
			return false
		}
	}
	if !read(json.Delim('{')) {
		return false
	}
	fields := 0
	for d.More() {
		fields++
		if fields > 8 {
			return false
		}
		key, err := d.Token()
		if err != nil {
			return false
		}
		switch key {
		case "schema", "name", "parent_digest", "request_digest", "plan_digest", "root_digest":
			if !scalar() {
				return false
			}
		case "files", "nodes":
			// A partial receipt has nil nodes. Canonical shape is checked again.
			token, e := d.Token()
			if e != nil {
				return false
			}
			if token == nil && key == "nodes" {
				continue
			}
			if token != json.Delim('[') {
				return false
			}
			limit := maxPublicationFiles
			if key == "nodes" {
				limit += typedindex.MaxInventoryDirectories + 1
			}
			count := 0
			for d.More() {
				count++
				if count > limit || !read(json.Delim('{')) {
					return false
				}
				members := 0
				for d.More() {
					members++
					if members > 4 {
						return false
					}
					field, e := d.Token()
					if e != nil {
						return false
					}
					name, ok := field.(string)
					if !ok || len(name) > 16 || !scalar() {
						return false
					}
				}
				if !read(json.Delim('}')) {
					return false
				}
			}
			if !read(json.Delim(']')) {
				return false
			}
		default:
			return false
		}
	}
	return fields == 8 && read(json.Delim('}'))
}

func publicationAuthority(parent, execution typedindex.Admission, plan string) bool {
	return parent.Purpose() == typedindex.Publish && execution.Purpose() == typedindex.Publish && executionAuthority(parent, execution, plan)
}

func executionAuthority(parent, execution typedindex.Admission, plan string) bool {
	if parent.Digest() == "" || execution.Digest() == "" || !publicationHash(plan) {
		return false
	}
	want, err := typedindex.PlannedSuccessor(context.Background(), parent, plan)
	return err == nil && execution.Request() == want
}

// publicationLayout rejects unknown paths and all per-kind and aggregate
// overflow before filesystem growth or allocation proportional to stored bytes.
func publicationLayout(files []typedindex.BundleFile) (layout, error) {
	if len(files) < 7 || len(files) > maxPublicationFiles {
		return layout{}, ErrCustody
	}
	entries := map[string]bool{".": true}
	counts, totals := map[string]int{}, map[string]int64{}
	var total, publication int64
	previous := ""
	for _, f := range files {
		if f.Path <= previous || len(f.Path) > 512 || !fs.ValidPath(f.Path) || path.Clean(f.Path) != f.Path || strings.ContainsAny(f.Path, "\\:") || f.Bytes < 0 || f.Executable || !publicationHash(f.Digest) {
			return layout{}, ErrCustody
		}
		for _, r := range f.Path {
			if r < 0x20 || r == 0x7f {
				return layout{}, ErrCustody
			}
		}
		for _, part := range strings.Split(f.Path, "/") {
			if len(part) > 255 {
				return layout{}, ErrCustody
			}
		}
		previous = f.Path
		kind, limit := "", int64(0)
		switch f.Path {
		case "parent.json", "request.json":
			kind, limit = f.Path, typedindex.MaxRequestBytes
		case "plan.json":
			kind, limit = f.Path, typedindex.MaxPlanBytes
		case "root.json":
			kind, limit = f.Path, typedindex.MaxRootBytes
		default:
			kind = strings.Split(f.Path, "/")[0]
			switch kind {
			case "members":
				limit = typedindex.MeasuredPolicy().SCIPBytes
			case "attempts":
				limit = typedindex.MaxAttemptBytes
			case "documents", "symbols":
				limit = typedindex.MaxRouteBytes
			case ".phebs-generated":
				limit = typedindex.MaxGeneratedBytes
			default:
				return layout{}, ErrCustody
			}
			if kind != ".phebs-generated" {
				ext := ".json"
				if kind == "members" {
					ext = ".scip"
				}
				if f.Path != kind+"/"+f.Digest[7:]+ext {
					return layout{}, ErrCustody
				}
			} else if !strings.HasPrefix(f.Path, ".phebs-generated/") || strings.Contains(f.Path, "/../") {
				return layout{}, ErrCustody
			}
		}
		if f.Bytes > limit || f.Bytes > maxInstalledPublicationBytes-total {
			return layout{}, ErrCustody
		}
		total += f.Bytes
		if kind != "parent.json" && kind != "request.json" && kind != "plan.json" {
			publication += f.Bytes
		}
		counts[kind]++
		totals[kind] += f.Bytes
		if _, exists := entries[f.Path]; exists {
			return layout{}, ErrCustody
		}
		entries[f.Path] = false
		for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
			if exists, ok := entries[dir]; ok && !exists {
				return layout{}, ErrCustody
			}
			entries[dir] = true
		}
	}
	for _, kind := range []string{"parent.json", "request.json", "plan.json", "root.json", "attempts", "documents", "symbols"} {
		if counts[kind] != 1 {
			return layout{}, ErrCustody
		}
	}
	if counts["members"] > typedindex.MaxSCIPMembers || counts[".phebs-generated"] > typedindex.MaxBundleDocuments || totals["members"] > typedindex.MaxSCIPAggregateBytes || totals[".phebs-generated"] > typedindex.MaxGeneratedAggregateBytes || publication > typedindex.MaxPublicationBytes {
		return layout{}, ErrCustody
	}
	dirs := []string{}
	for name, directory := range entries {
		if directory {
			dirs = append(dirs, name)
		}
	}
	if len(dirs) > typedindex.MaxInventoryDirectories+1 {
		return layout{}, ErrCustody
	}
	slices.Sort(dirs)
	return layout{slices.Clone(files), dirs, entries}, nil
}

// AcquirePublicationMutation is the exclusive half of the publication reader
// lease. The parent must already be private 0700; this never creates ancestry.
// The caller retains this guard through its final rename/unlink and directory sync.
func AcquirePublicationMutation(ctx context.Context, privateParent string) (func(), error) {
	return publicationLease(ctx, privateParent, true, false)
}

func publicationLease(ctx context.Context, parent string, exclusive, create bool) (func(), error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrCustody
	}
	dir, err := openDirectory(parent, true)
	if err != nil {
		return nil, ErrCustody
	}
	defer func() { _ = dir.Close() }()
	before, err := dir.Stat()
	if err != nil {
		return nil, ErrCustody
	}
	file, err := openRelative(dir, publicationLock, false)
	if err != nil && create {
		file, err = createFile(dir, publicationLock)
		if err == nil {
			err = errors.Join(file.Sync(), file.Close(), dir.Sync())
			if err == nil {
				file, err = openRelative(dir, publicationLock, false)
			}
		}
	}
	if err != nil {
		return nil, ErrCustody
	}
	defer func() { _ = file.Close() }()
	lockInfo, err := file.Stat()
	if err != nil || lockInfo.Size() != 0 || lockInfo.Mode() != 0600 {
		return nil, ErrCustody
	}
	acquire := focusedindex.AcquireMutationLock
	if exclusive {
		acquire = focusedindex.AcquireExclusiveMutationLock
	}
	release, err := acquire(ctx, parent)
	if err != nil {
		return nil, err
	}
	// Private ancestry excludes workers; also detect a path/inode substitution
	// around the existing path-based kernel-lock API.
	again, err := openDirectory(parent, true)
	if err == nil {
		var info os.FileInfo
		info, err = again.Stat()
		if err == nil && !os.SameFile(before, info) {
			err = ErrCustody
		}
		var lock *os.File
		lock, err = openLockAgain(again, lockInfo, err)
		if lock != nil {
			err = errors.Join(err, lock.Close())
		}
		err = errors.Join(err, again.Close())
	}
	if err != nil {
		release()
		return nil, ErrCustody
	}
	return release, nil
}
func openLockAgain(dir *os.File, before os.FileInfo, prior error) (*os.File, error) {
	if prior != nil {
		return nil, prior
	}
	f, err := openRelative(dir, publicationLock, false)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !os.SameFile(before, st) {
		return f, ErrCustody
	}
	return f, nil
}

// InstallPublication durably installs a complete verified bundle. Its returned
// receipt is custody only; no current pointer is committed. Aggregate reservation
// across attempts and durable receipt persistence remain caller obligations.
func InstallPublication(ctx context.Context, privateParent string, parent, execution typedindex.Admission, plan typedindex.PackagePlan, bundle typedindex.Bundle, gate *lifecycle.Gate) (PublicationReceipt, error) {
	return installPublication(ctx, privateParent, parent, execution, plan, bundle, gate, capacity)
}
func installPublication(ctx context.Context, privateParent string, parent, execution typedindex.Admission, plan typedindex.PackagePlan, bundle typedindex.Bundle, gate *lifecycle.Gate, probe func(*os.File) (space, error)) (receipt PublicationReceipt, err error) {
	if ctx == nil || !publicationAuthority(parent, execution, plan.Digest()) || bundle.RootDigest() == "" {
		return receipt, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	data := map[string][]byte{"plan.json": plan.Bytes(), "root.json": bundle.RootBytes()}
	data["parent.json"], _ = json.Marshal(parent.Request())
	data["request.json"], _ = json.Marshal(execution.Request())
	for _, name := range bundle.Names() {
		data[name] = bundle.Content(name)
	}
	files := make([]typedindex.BundleFile, 0, len(data))
	for name, raw := range data {
		files = append(files, typedindex.BundleFile{Path: name, Bytes: int64(len(raw)), Digest: publicationDigest(raw)})
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	tree, err := publicationLayout(files)
	if err != nil {
		return receipt, err
	}
	if _, err = verifyPublication(ctx, parent, execution, plan.Digest(), bundle.RootDigest(), data); err != nil {
		return receipt, err
	}
	dir, err := openDirectory(privateParent, true)
	if err != nil {
		return receipt, ErrCustody
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	if err = publicationCapacity(ctx, dir, tree, gate, probe); err != nil {
		return receipt, err
	}
	release, err := publicationLease(ctx, privateParent, true, true)
	if err != nil {
		return receipt, err
	}
	defer release()
	// Waiting on readers may have taken time; observe capacity again under the
	// exclusive guard before stage growth. This still is not a reservation.
	if err = publicationCapacity(ctx, dir, tree, gate, probe); err != nil {
		return receipt, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return receipt, err
	}
	final := "bundle-" + hex.EncodeToString(token[:])
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	if err = mkdir(dir, final+".stage"); err != nil {
		return receipt, ErrCustody
	}
	receipt = PublicationReceipt{Schema: publicationSchema, Name: final + ".stage", ParentDigest: parent.Digest(), RequestDigest: execution.Digest(), PlanDigest: plan.Digest(), RootDigest: bundle.RootDigest(), Files: files}
	if err = dir.Sync(); err != nil {
		return receipt, ErrCustody
	}
	root, err := openRelative(dir, receipt.Name, true)
	if err != nil {
		return receipt, ErrCustody
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for _, name := range tree.directories {
		if err = ctx.Err(); err != nil {
			return receipt, err
		}
		if name != "." && mkdir(root, name) != nil {
			return receipt, ErrCustody
		}
	}
	for _, row := range files {
		if err = writePublicationFile(ctx, root, row, data[row.Path]); err != nil {
			return receipt, err
		}
		if err = custodybytes.Checkpoint(ctx); err != nil {
			return receipt, err
		}
	}
	for i := len(tree.directories) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return receipt, err
		}
		child, e := openRelative(root, tree.directories[i], true)
		if e != nil {
			return receipt, ErrCustody
		}
		if err = errors.Join(child.Chmod(0555), child.Sync(), child.Close()); err != nil {
			return receipt, err
		}
	}
	receipt.Nodes, err = inspect(ctx, root, tree, true)
	if err != nil {
		return receipt, err
	}
	if err = custodybytes.Checkpoint(ctx); err != nil {
		return receipt, err
	}
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	if err = renameExclusive(dir, receipt.Name, final); err != nil {
		return receipt, ErrCustody
	}
	receipt.Name = final
	return receipt, dir.Sync()
}

func publicationCapacity(ctx context.Context, dir *os.File, tree layout, gate *lifecycle.Gate, probe func(*os.File) (space, error)) error {
	available, err := probe(dir)
	if err != nil {
		return ErrCustody
	}
	needed, err := neededSpace(tree, available.block)
	if err != nil {
		return err
	}
	needed += available.block // the fixed kernel lock may need its first inode
	if _, err = gate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(available.total), AvailableBytes: int64(available.bytes), UsedBytes: int64(available.total - available.bytes)}, int64(needed)); err != nil {
		return err
	}
	if available.bytes < needed || available.inodes < uint64(len(tree.entries)+1) {
		return lifecycle.ErrPressureRefusal
	}
	return nil
}

func writePublicationFile(ctx context.Context, root *os.File, row typedindex.BundleFile, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := createFile(root, row.Path)
	if err != nil {
		return ErrCustody
	}
	for offset := 0; offset < len(data) && err == nil; {
		if err = ctx.Err(); err != nil {
			break
		}
		n := min(32<<10, len(data)-offset)
		var written int
		written, err = f.Write(data[offset : offset+n])
		if written != n {
			err = ErrCustody
		}
		offset += written
	}
	before, statErr := f.Stat()
	err = errors.Join(err, statErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	f, err = openRelative(root, row.Path, false)
	if err != nil {
		return ErrCustody
	}
	after, statErr := f.Stat()
	if statErr != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return ErrCustody
	}
	if err = ctx.Err(); err != nil {
		_ = f.Close()
		return err
	}
	return errors.Join(f.Chmod(0444), f.Sync(), f.Close())
}

func verifyPublication(ctx context.Context, parent, execution typedindex.Admission, planDigest, rootDigest string, data map[string][]byte) (typedindex.BundleRoot, error) {
	var control typedindex.BundleRoot
	p, err := typedindex.DecodePackagePlan(ctx, parent, data["plan.json"], planDigest)
	if err != nil || publicationDigest(data["root.json"]) != rootDigest {
		return control, ErrCustody
	}
	for name, admission := range map[string]typedindex.Admission{"parent.json": parent, "request.json": execution} {
		want, e := json.Marshal(admission.Request())
		if e != nil || !bytes.Equal(data[name], want) {
			return control, ErrCustody
		}
	}
	if json.Unmarshal(data["root.json"], &control) != nil {
		return control, ErrCustody
	}
	contents := make(map[string][]byte, len(data)-4)
	for name, raw := range data {
		if strings.Contains(name, "/") {
			contents[name] = raw
		}
	}
	_, err = typedindex.VerifyBundle(ctx, execution, p, contents[control.Attempt.Name], data["root.json"], contents)
	return control, err
}

// OpenPublication revalidates every declared byte and inode once while acquiring
// a shared lease. Expected identities and admissions come from trusted store
// authority. No receipt field or cached flag independently establishes readiness.
func OpenPublication(ctx context.Context, privateParent string, parent, execution typedindex.Admission, planDigest, rootDigest string, receipt PublicationReceipt) (_ *Publication, err error) {
	if ctx == nil || !publicationAuthority(parent, execution, planDigest) || !publicationHash(rootDigest) || receipt.Schema != publicationSchema || !publicationName(receipt.Name) || receipt.ParentDigest != parent.Digest() || receipt.RequestDigest != execution.Digest() || receipt.PlanDigest != planDigest || receipt.RootDigest != rootDigest {
		return nil, ErrCustody
	}
	tree, err := validatePublicationReceipt(receipt)
	if err != nil || len(receipt.Nodes) != len(tree.entries) {
		return nil, ErrCustody
	}
	release, err := publicationLease(ctx, privateParent, false, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	dir, err := openDirectory(privateParent, true)
	if err != nil {
		return nil, ErrCustody
	}
	root, openErr := openRelative(dir, receipt.Name, true)
	err = errors.Join(openErr, dir.Close())
	if err != nil {
		if root != nil {
			_ = root.Close()
		}
		return nil, ErrCustody
	}
	defer func() {
		if err != nil {
			_ = root.Close()
		}
	}()
	observed, err := inspect(ctx, root, tree, true)
	if err != nil || !slices.Equal(observed, receipt.Nodes) {
		return nil, ErrCustody
	}
	p := &Publication{root: root, release: release, files: map[string]typedindex.BundleFile{}, nodes: map[string]Node{}}
	for _, n := range receipt.Nodes {
		p.nodes[n.Path] = n
	}
	data := make(map[string][]byte, len(tree.files))
	for _, row := range tree.files {
		data[row.Path], err = readPublicationFile(ctx, root, row, p.nodes[row.Path])
		if err != nil {
			return nil, err
		}
		if strings.Contains(row.Path, "/") {
			p.files[row.Path] = row
		}
	}
	p.control, err = verifyPublication(ctx, parent, execution, planDigest, rootDigest, data)
	if err != nil {
		return nil, err
	}
	observed, err = inspect(ctx, root, tree, true)
	if err != nil || !slices.Equal(observed, receipt.Nodes) {
		return nil, ErrCustody
	}
	if err = custodybytes.Checkpoint(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func readPublicationFile(ctx context.Context, root *os.File, row typedindex.BundleFile, node Node) (_ []byte, err error) {
	f, err := openRelative(root, row.Path, false)
	if err != nil {
		return nil, ErrCustody
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	before, err := fileInfo(f, row, true)
	if err != nil || before.device != node.Device || before.inode != node.Inode {
		return nil, ErrCustody
	}
	var data bytes.Buffer
	data.Grow(int(row.Bytes))
	if err = transfer(ctx, f, &data, row.Bytes); err != nil {
		return nil, err
	}
	after, err := fileInfo(f, row, true)
	if err != nil || before != after || publicationDigest(data.Bytes()) != row.Digest {
		return nil, ErrCustody
	}
	return data.Bytes(), nil
}

func (p *Publication) Root() typedindex.BundleRoot { return p.control }
func (p *Publication) ReadMember(ctx context.Context, name string) ([]byte, error) {
	if p == nil || ctx == nil {
		return nil, ErrCustody
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	row, ok := p.files[name]
	if p.root == nil || !ok {
		return nil, ErrCustody
	}
	return readPublicationFile(ctx, p.root, row, p.nodes[name])
}
func (p *Publication) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.root == nil {
		return nil
	}
	err := p.root.Close()
	p.root = nil
	p.release()
	p.release = nil
	return err
}
