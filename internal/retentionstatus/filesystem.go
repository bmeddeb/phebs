package retentionstatus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

var (
	errObservationBudget = errors.New("retention filesystem observation budget exhausted")
	errStatBudget        = errors.New("retention filesystem stat budget exhausted")
	errMetadataBudget    = errors.New("retention manifest metadata budget exhausted")
)

type collectionBudget struct {
	policy                collectorPolicy
	componentObservations map[store.RetentionComponent]int
	aggregateObservations int
	stats                 int
	reservedStats         int
	metadataBytes         int64
	descriptors           int
	hooks                 collectorHooks
}

func newCollectionBudget(policy collectorPolicy, hooks collectorHooks) *collectionBudget {
	return &collectionBudget{
		policy: policy, componentObservations: make(map[store.RetentionComponent]int, 4),
		hooks: hooks,
	}
}

func (budget *collectionBudget) observe(component store.RetentionComponent) error {
	componentLimit := budget.policy.componentObservations[component]
	if componentLimit <= 0 || budget.componentObservations[component] >= componentLimit ||
		budget.aggregateObservations >= budget.policy.aggregateObservations {
		return errObservationBudget
	}
	budget.componentObservations[component]++
	budget.aggregateObservations++
	return nil
}

func (budget *collectionBudget) stat() error {
	if budget.stats >= budget.policy.aggregateStats-budget.reservedStats {
		return errStatBudget
	}
	budget.stats++
	return nil
}

func (budget *collectionBudget) reserveStats(count int) error {
	if count < 0 || budget.stats > budget.policy.aggregateStats-budget.reservedStats-count {
		return errStatBudget
	}
	budget.reservedStats += count
	return nil
}

func (budget *collectionBudget) useReservedStat() error {
	if budget.reservedStats <= 0 || budget.stats >= budget.policy.aggregateStats {
		return errStatBudget
	}
	budget.reservedStats--
	budget.stats++
	return nil
}

func (budget *collectionBudget) metadata(size int64) error {
	if size < 0 || size > budget.policy.aggregateMetadata-budget.metadataBytes {
		return errMetadataBudget
	}
	budget.metadataBytes += size
	if budget.hooks.metadataObserved != nil {
		budget.hooks.metadataObserved(budget.metadataBytes)
	}
	return nil
}

func (budget *collectionBudget) openedDescriptor() error {
	if budget.descriptors >= budget.policy.maxDescriptors {
		return fmt.Errorf("retention filesystem descriptor bound %d exceeded", budget.policy.maxDescriptors)
	}
	budget.descriptors++
	if budget.hooks.descriptorDelta != nil {
		budget.hooks.descriptorDelta(1)
	}
	return nil
}

func (budget *collectionBudget) closedDescriptor() {
	budget.descriptors--
	if budget.hooks.descriptorDelta != nil {
		budget.hooks.descriptorDelta(-1)
	}
}

type stableRoot struct {
	path   string
	info   os.FileInfo
	root   *os.Root
	budget *collectionBudget
	closed bool
}

func openStableRoot(
	ctx context.Context,
	path string,
	budget *collectionBudget,
) (*stableRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if budget == nil {
		return nil, errors.New("retention filesystem budget is nil")
	}
	if err := budget.stat(); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention root %q is not a real directory", path)
	}
	if budget.hooks.beforeDirectoryOpen != nil {
		budget.hooks.beforeDirectoryOpen(path)
	}
	// os.OpenRoot validates the returned descriptor with an internal fstat.
	if err := budget.stat(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directoryTraversal(path))
	if err != nil {
		return nil, err
	}
	if err := budget.openedDescriptor(); err != nil {
		_ = root.Close()
		return nil, err
	}
	closeRoot := func() {
		_ = root.Close()
		budget.closedDescriptor()
	}
	if err := budget.stat(); err != nil {
		closeRoot()
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil {
		closeRoot()
		return nil, err
	}
	if !sameDirectory(before, opened) {
		closeRoot()
		return nil, fmt.Errorf("retention root %q changed while opening", path)
	}
	if budget.hooks.afterDirectoryOpen != nil {
		budget.hooks.afterDirectoryOpen(path)
	}
	if err := budget.stat(); err != nil {
		closeRoot()
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !sameDirectory(opened, after) {
		closeRoot()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("retention root %q changed while opening", path)
	}
	return &stableRoot{path: path, info: opened, root: root, budget: budget}, nil
}

func (root *stableRoot) close() error {
	return root.closeWithFence(false)
}

func (root *stableRoot) closeReserved() error {
	return root.closeWithFence(true)
}

func (root *stableRoot) closeWithFence(reserved bool) error {
	if root == nil || root.closed {
		return nil
	}
	root.closed = true
	closeErr := root.root.Close()
	root.budget.closedDescriptor()
	var budgetErr error
	if reserved {
		budgetErr = root.budget.useReservedStat()
	} else {
		budgetErr = root.budget.stat()
	}
	if budgetErr != nil {
		return errors.Join(closeErr, budgetErr)
	}
	after, statErr := os.Lstat(root.path)
	if statErr != nil || !sameDirectory(root.info, after) {
		if statErr == nil {
			statErr = fmt.Errorf("retention root %q changed during collection", root.path)
		}
	}
	return errors.Join(closeErr, statErr)
}

type canonicalRoot struct {
	path     string
	relative string
	info     os.FileInfo
	root     *os.Root
	anchor   *stableRoot
	fences   []directoryFence
	budget   *collectionBudget
	closed   bool
}

func openCanonicalRootAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
) (*canonicalRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if anchor == nil || anchor.closed {
		return nil, errors.New("retention canonical root anchor is unavailable")
	}
	if filepath.Base(relative) != relative || relative == "." {
		return nil, errors.New("retention canonical root name is not a basename")
	}
	if err := anchor.budget.stat(); err != nil {
		return nil, err
	}
	before, err := anchor.root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention canonical root %q is not a real directory", relative)
	}
	if anchor.budget.hooks.beforeDirectoryOpen != nil {
		anchor.budget.hooks.beforeDirectoryOpen(pathFromRelative(anchor.path, relative))
	}
	// Root.OpenRoot constructs the returned Root through an internal fstat.
	if err := anchor.budget.stat(); err != nil {
		return nil, err
	}
	root, err := anchor.root.OpenRoot(directoryTraversal(relative))
	if err != nil {
		return nil, err
	}
	if err := anchor.budget.openedDescriptor(); err != nil {
		_ = root.Close()
		return nil, err
	}
	closeRoot := func(result error) error {
		closeErr := root.Close()
		anchor.budget.closedDescriptor()
		return errors.Join(result, closeErr)
	}
	if err := anchor.budget.stat(); err != nil {
		return nil, closeRoot(err)
	}
	opened, err := root.Stat(".")
	if err != nil || !sameDirectory(before, opened) {
		if err == nil {
			err = fmt.Errorf("retention canonical root %q changed while opening", relative)
		}
		return nil, closeRoot(err)
	}
	path := filepath.Join(anchor.path, relative)
	if anchor.budget.hooks.afterDirectoryOpen != nil {
		anchor.budget.hooks.afterDirectoryOpen(path)
	}
	if err := anchor.budget.stat(); err != nil {
		return nil, closeRoot(err)
	}
	current, err := anchor.root.Lstat(relative)
	if err != nil || !sameDirectory(opened, current) {
		if err == nil {
			err = fmt.Errorf("retention canonical root %q changed after opening", relative)
		}
		return nil, closeRoot(err)
	}
	fence := directoryFence{path: path, relative: relative, info: opened}
	return &canonicalRoot{
		path: path, relative: relative, info: opened, root: root,
		anchor: anchor, fences: []directoryFence{fence}, budget: anchor.budget,
	}, nil
}

func (root *canonicalRoot) openChild(
	ctx context.Context,
	name string,
) (*canonicalRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == nil || root.closed || filepath.Base(name) != name || name == "." {
		return nil, errors.New("retention canonical child root is invalid")
	}
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	before, err := root.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention canonical child %q is not a real directory", name)
	}
	path := filepath.Join(root.path, name)
	if root.budget.hooks.beforeNestedOpen != nil {
		root.budget.hooks.beforeNestedOpen(path)
	}
	if root.budget.hooks.beforeDirectoryOpen != nil {
		root.budget.hooks.beforeDirectoryOpen(path)
	}
	// Root.OpenRoot constructs the returned Root through an internal fstat.
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	childRoot, err := root.root.OpenRoot(directoryTraversal(name))
	if err != nil {
		return nil, err
	}
	if err := root.budget.openedDescriptor(); err != nil {
		_ = childRoot.Close()
		return nil, err
	}
	closeRoot := func(result error) error {
		closeErr := childRoot.Close()
		root.budget.closedDescriptor()
		return errors.Join(result, closeErr)
	}
	if root.budget.hooks.afterNestedOpen != nil {
		root.budget.hooks.afterNestedOpen(path)
	}
	if err := root.budget.stat(); err != nil {
		return nil, closeRoot(err)
	}
	opened, err := childRoot.Stat(".")
	if err != nil || !sameDirectory(before, opened) {
		if err == nil {
			err = fmt.Errorf("retention canonical child %q changed while opening", name)
		}
		return nil, closeRoot(err)
	}
	if err := root.budget.stat(); err != nil {
		return nil, closeRoot(err)
	}
	current, err := root.root.Lstat(name)
	if err != nil || !sameDirectory(opened, current) {
		if err == nil {
			err = fmt.Errorf("retention canonical child %q changed after opening", name)
		}
		return nil, closeRoot(err)
	}
	relative := filepath.Join(root.relative, name)
	fences := make([]directoryFence, len(root.fences), len(root.fences)+1)
	copy(fences, root.fences)
	fences = append(fences, directoryFence{path: path, relative: relative, info: opened})
	return &canonicalRoot{
		path: path, relative: relative, info: opened, root: childRoot,
		anchor: root.anchor, fences: fences, budget: root.budget,
	}, nil
}

func (root *canonicalRoot) verifyFences() error {
	for _, fence := range root.fences {
		if err := root.budget.stat(); err != nil {
			return err
		}
		current, err := root.anchor.root.Lstat(fence.relative)
		if err != nil || !sameDirectory(fence.info, current) {
			if err == nil {
				err = fmt.Errorf("retention canonical root %q changed", fence.path)
			}
			return err
		}
	}
	return nil
}

func (root *canonicalRoot) readRegular(
	ctx context.Context,
	name string,
	maxBytes int64,
) (raw []byte, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == nil || root.closed || filepath.Base(name) != name || name == "." {
		return nil, errors.New("retention canonical metadata name is invalid")
	}
	if err := root.verifyFences(); err != nil {
		return nil, err
	}
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	before, err := root.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() < 0 || before.Size() > maxBytes {
		return nil, fmt.Errorf("retention metadata %q is not a bounded regular file", name)
	}
	if err := root.budget.metadata(before.Size()); err != nil {
		return nil, err
	}
	path := filepath.Join(root.path, name)
	if root.budget.hooks.beforeRegularOpen != nil {
		root.budget.hooks.beforeRegularOpen(path)
	}
	// Root.OpenFile may classify the descriptor with an internal fstat.
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	file, err := openRootedRegular(root.root, name)
	if err != nil {
		return nil, err
	}
	if err := root.budget.openedDescriptor(); err != nil {
		_ = file.Close()
		return nil, err
	}
	defer func() {
		closeErr := file.Close()
		root.budget.closedDescriptor()
		if root.budget.hooks.fileCloseError != nil {
			closeErr = errors.Join(closeErr, root.budget.hooks.fileCloseError(name))
		}
		resultErr = errors.Join(resultErr, closeErr)
	}()
	if root.budget.hooks.afterRegularOpen != nil {
		root.budget.hooks.afterRegularOpen(path)
	}
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !sameRegular(before, opened) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("retention metadata %q changed while opening", name)
	}
	if root.budget.hooks.beforeRegularRead != nil {
		root.budget.hooks.beforeRegularRead(path)
	}
	raw, err = io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, before.Size()))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != before.Size() || int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("retention metadata %q changed length while reading", name)
	}
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	openedAfter, err := file.Stat()
	if err != nil || !sameRegular(opened, openedAfter) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("retention metadata %q changed while reading", name)
	}
	if err := root.budget.stat(); err != nil {
		return nil, err
	}
	pathAfter, err := root.root.Lstat(name)
	if err != nil || !sameRegular(opened, pathAfter) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("retention metadata %q path changed while reading", name)
	}
	if err := root.verifyFences(); err != nil {
		return nil, err
	}
	return raw, nil
}

func (root *canonicalRoot) close() error {
	if root == nil || root.closed {
		return nil
	}
	root.closed = true
	closeErr := root.root.Close()
	root.budget.closedDescriptor()
	return errors.Join(closeErr, root.verifyFences())
}

type stableDirectory struct {
	path     string
	relative string
	info     os.FileInfo
	file     *os.File
	anchor   *stableRoot
	budget   *collectionBudget
	closed   bool
}

type directoryFence struct {
	path     string
	relative string
	info     os.FileInfo
}

func openStableDirectoryAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	known os.FileInfo,
) (*stableDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if anchor == nil || anchor.closed || anchor.root == nil {
		return nil, errors.New("retention directory anchor is unavailable")
	}
	relative, err := cleanRelativePath(relative)
	if err != nil {
		return nil, err
	}
	before := known
	if before == nil && relative == "." {
		before = anchor.info
	}
	if before == nil {
		if err := anchor.budget.stat(); err != nil {
			return nil, err
		}
		before, err = anchor.root.Lstat(relative)
		if err != nil {
			return nil, err
		}
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention directory %q is not a real directory", relative)
	}
	path := pathFromRelative(anchor.path, relative)
	if anchor.budget.hooks.beforeDirectoryOpen != nil {
		anchor.budget.hooks.beforeDirectoryOpen(path)
	}
	// Root.Open may classify the descriptor with an internal fstat.
	if err := anchor.budget.stat(); err != nil {
		return nil, err
	}
	stream, err := anchor.root.Open(directoryTraversal(relative))
	if err != nil {
		return nil, err
	}
	if err := anchor.budget.openedDescriptor(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	closeStream := func(result error) error {
		closeErr := stream.Close()
		anchor.budget.closedDescriptor()
		return errors.Join(result, closeErr)
	}
	if known != nil && relative != "." && anchor.budget.hooks.afterNestedOpen != nil {
		anchor.budget.hooks.afterNestedOpen(filepath.Join(anchor.path, relative))
	}
	if err := anchor.budget.stat(); err != nil {
		return nil, closeStream(err)
	}
	opened, err := stream.Stat()
	if err != nil || !sameDirectory(before, opened) {
		if err == nil {
			err = fmt.Errorf("retention directory %q changed while opening", relative)
		}
		return nil, closeStream(err)
	}
	if anchor.budget.hooks.afterDirectoryOpen != nil {
		anchor.budget.hooks.afterDirectoryOpen(path)
	}
	if err := anchor.budget.stat(); err != nil {
		return nil, closeStream(err)
	}
	current, err := anchor.root.Lstat(relative)
	if err != nil || !sameDirectory(opened, current) {
		if err == nil {
			err = fmt.Errorf("retention directory %q changed after opening", relative)
		}
		return nil, closeStream(err)
	}
	return &stableDirectory{
		path: path, relative: relative, info: opened, file: stream,
		anchor: anchor, budget: anchor.budget,
	}, nil
}

func (directory *stableDirectory) close() error {
	if directory == nil || directory.closed {
		return nil
	}
	directory.closed = true
	if directory.budget.hooks.beforeDirectoryClose != nil {
		directory.budget.hooks.beforeDirectoryClose(directory.path)
	}
	closeErr := directory.file.Close()
	directory.budget.closedDescriptor()
	if err := directory.budget.stat(); err != nil {
		return errors.Join(closeErr, err)
	}
	after, statErr := directory.anchor.root.Lstat(directory.relative)
	if statErr != nil || !sameDirectory(directory.info, after) {
		if statErr == nil {
			statErr = fmt.Errorf(
				"retention directory %q changed during collection", directory.path,
			)
		}
	}
	return errors.Join(closeErr, statErr)
}

func (directory *stableDirectory) openChildDirectory(
	ctx context.Context,
	name string,
	validated os.FileInfo,
) (*stableDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if directory == nil || directory.anchor == nil || directory.anchor.closed {
		return nil, errors.New("retention nested directory has no stable parent root")
	}
	if filepath.Base(name) != name || name == "." || !validated.IsDir() ||
		validated.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention nested directory %q is not a validated real directory", name)
	}
	relative := filepath.Join(directory.relative, name)
	path := filepath.Join(directory.path, name)
	if directory.budget.hooks.beforeNestedOpen != nil {
		directory.budget.hooks.beforeNestedOpen(path)
	}
	child, err := openStableDirectoryAt(ctx, directory.anchor, relative, validated)
	if err != nil {
		return nil, err
	}
	if err := directory.budget.stat(); err != nil {
		return nil, errors.Join(err, child.close())
	}
	parentCurrent, err := directory.anchor.root.Lstat(directory.relative)
	if err != nil || !sameDirectory(directory.info, parentCurrent) {
		if err == nil {
			err = fmt.Errorf("retention parent directory %q changed while opening child", directory.path)
		}
		return nil, errors.Join(err, child.close())
	}
	return child, nil
}

func (directory *stableDirectory) readBatch(
	component store.RetentionComponent,
) ([]string, error) {
	componentRemaining := directory.budget.policy.componentObservations[component] -
		directory.budget.componentObservations[component]
	aggregateRemaining := directory.budget.policy.aggregateObservations -
		directory.budget.aggregateObservations
	if componentRemaining <= 0 || aggregateRemaining <= 0 {
		return nil, errObservationBudget
	}
	limit := min(
		directory.budget.policy.readDirBatch,
		componentRemaining,
		aggregateRemaining,
	)
	// Readdirnames is deliberately names-only. os.File.ReadDir eagerly lstats
	// rooted entries on some platforms and can perform unbounded extra lstats
	// for concurrently vanished names. Recognized names are lstat'd explicitly
	// through the retained root by stableEntryInfo under the stat budget. One
	// conservative slot covers the Windows error-classification Stat fallback.
	if err := directory.budget.stat(); err != nil {
		return nil, err
	}
	names, err := directory.file.Readdirnames(limit)
	for range names {
		if observeErr := directory.budget.observe(component); observeErr != nil {
			return names, errors.Join(err, observeErr)
		}
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return names, err
}

func cleanRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return "", errors.New("retention path is not relative")
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("retention path escapes its root")
	}
	return cleaned, nil
}

func directoryTraversal(path string) string {
	return path + string(filepath.Separator) + "."
}

func pathFromRelative(anchor string, relative string) string {
	if relative == "." {
		return anchor
	}
	return filepath.Join(anchor, relative)
}

func sameDirectory(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right)
}

func sameRegular(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() &&
		right.Mode().IsRegular() && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime()) &&
		os.SameFile(left, right)
}

type fileIdentity struct {
	name string
	size int64
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if len(destination) > 64<<10 {
		destination = destination[:64<<10]
	}
	return reader.reader.Read(destination)
}

type fileScan struct {
	identities []fileIdentity
	scanned    int
	apparent   int64
	truncated  bool
	complete   bool
	err        error
}

func (scan *fileScan) selectIdentity(
	request store.RetentionComponentRequest,
	name string,
	size int64,
) error {
	if size < 0 || size > math.MaxInt64-scan.apparent {
		return errors.New("retention apparent-file byte total overflows int64")
	}
	scan.scanned++
	if scan.scanned <= request.ReportedIdentities {
		scan.identities = append(scan.identities, fileIdentity{name: name, size: size})
		scan.apparent += size
	}
	if scan.scanned == request.ScanIdentities {
		scan.truncated = true
		return io.EOF
	}
	return nil
}

func (scan fileScan) componentResult(
	component store.RetentionComponent,
) ComponentResult {
	result := ComponentResult{
		Component:         component,
		ScannedIdentities: scan.scanned,
		ByteMetrics:       []ByteMetric{byteMetric(ApparentFile, unavailableMetric())},
	}
	switch {
	case scan.truncated:
		reported := int64(len(scan.identities))
		result.Count = metric(reported, LowerBound)
		result.ByteMetrics[0].Metric = metric(scan.apparent, LowerBound)
		result.Truncated = true
		if scan.err != nil {
			result.Diagnostics = []error{scan.err}
		}
	case scan.complete:
		reported := int64(scan.scanned)
		result.Count = metric(reported, Exact)
		result.ByteMetrics[0].Metric = metric(scan.apparent, Exact)
	case scan.scanned > 0:
		reported := int64(scan.scanned)
		result.Count = metric(reported, LowerBound)
		result.ByteMetrics[0].Metric = metric(scan.apparent, LowerBound)
		if scan.err != nil {
			result.Diagnostics = []error{scan.err}
		}
	default:
		result.Count = unavailableMetric()
		if scan.err != nil {
			result.Err = scan.err
		}
	}
	return result
}

func scanFlatFiles(
	ctx context.Context,
	path string,
	request store.RetentionComponentRequest,
	budget *collectionBudget,
	owned func(string) bool,
) (scan fileScan) {
	return scanWithStableRoot(ctx, path, budget, func(anchor *stableRoot) fileScan {
		return scanFlatFilesAt(ctx, anchor, ".", request, owned)
	})
}

func scanFlatFilesAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	request store.RetentionComponentRequest,
	owned func(string) bool,
) (scan fileScan) {
	directory, err := openStableDirectoryAt(ctx, anchor, relative, nil)
	if errors.Is(err, os.ErrNotExist) {
		return fileScan{complete: true}
	}
	if err != nil {
		return fileScan{err: err}
	}
	defer func() {
		if closeErr := directory.close(); closeErr != nil {
			scan.complete = false
			scan.err = errors.Join(scan.err, closeErr)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			scan.err = err
			return scan
		}
		names, readErr := directory.readBatch(request.Component)
		if readErr != nil {
			scan.err = readErr
			return scan
		}
		if len(names) == 0 {
			scan.complete = true
			return scan
		}
		for _, name := range names {
			if !owned(name) {
				continue
			}
			info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
			if infoErr != nil {
				scan.err = infoErr
				return scan
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				scan.err = fmt.Errorf("retention artifact %q is not a regular file", name)
				return scan
			}
			if err := scan.selectIdentity(request, name, info.Size()); errors.Is(err, io.EOF) {
				return scan
			} else if err != nil {
				scan.err = err
				return scan
			}
		}
	}
}

func scanWithStableRoot(
	ctx context.Context,
	path string,
	budget *collectionBudget,
	scanRoot func(*stableRoot) fileScan,
) (scan fileScan) {
	anchor, err := openStableRoot(ctx, path, budget)
	if errors.Is(err, os.ErrNotExist) {
		return fileScan{complete: true}
	}
	if err != nil {
		return fileScan{err: err}
	}
	scan = scanRoot(anchor)
	if closeErr := anchor.close(); closeErr != nil {
		scan.complete = false
		scan.err = errors.Join(scan.err, closeErr)
	}
	return scan
}

func stableEntryInfo(
	ctx context.Context,
	directory *stableDirectory,
	name string,
	budget *collectionBudget,
) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("retention artifact name %q is not a basename", name)
	}
	if err := budget.stat(); err != nil {
		return nil, err
	}
	relative := filepath.Join(directory.relative, name)
	info, err := directory.anchor.root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention artifact %q is a symbolic link", name)
	}
	path := filepath.Join(directory.path, name)
	if budget.hooks.afterEntryInfo != nil {
		budget.hooks.afterEntryInfo(path)
	}
	if err := budget.stat(); err != nil {
		return nil, err
	}
	current, err := directory.anchor.root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !sameDirectory(info, current) {
			return nil, fmt.Errorf("retention directory entry %q changed", name)
		}
		return info, nil
	}
	if !sameRegular(info, current) {
		return nil, fmt.Errorf("retention artifact %q changed", name)
	}
	return info, nil
}

func scanCandidateFiles(
	ctx context.Context,
	root string,
	request store.RetentionComponentRequest,
	budget *collectionBudget,
) fileScan {
	return scanFlatFiles(ctx, root, request, budget, candidate.IsRetentionArtifactName)
}

func scanCandidateFilesAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	request store.RetentionComponentRequest,
) fileScan {
	return scanFlatFilesAt(ctx, anchor, relative, request, candidate.IsRetentionArtifactName)
}

func scanFocusedFiles(
	ctx context.Context,
	root string,
	request store.RetentionComponentRequest,
	budget *collectionBudget,
) fileScan {
	return scanFlatFiles(ctx, root, request, budget, focusedindex.IsRetentionArtifactName)
}

func scanFocusedFilesAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	request store.RetentionComponentRequest,
) fileScan {
	return scanFlatFilesAt(ctx, anchor, relative, request, focusedindex.IsRetentionArtifactName)
}

func scanResolverFiles(
	ctx context.Context,
	root string,
	request store.RetentionComponentRequest,
	budget *collectionBudget,
) (scan fileScan) {
	return scanWithStableRoot(ctx, root, budget, func(anchor *stableRoot) fileScan {
		return scanResolverFilesAt(ctx, anchor, ".", request)
	})
}

func scanResolverFilesAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	request store.RetentionComponentRequest,
) (scan fileScan) {
	directory, err := openStableDirectoryAt(ctx, anchor, relative, nil)
	if errors.Is(err, os.ErrNotExist) {
		return fileScan{complete: true}
	}
	if err != nil {
		return fileScan{err: err}
	}
	defer func() {
		if closeErr := directory.close(); closeErr != nil {
			scan.complete = false
			scan.err = errors.Join(scan.err, closeErr)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			scan.err = err
			return scan
		}
		names, readErr := directory.readBatch(request.Component)
		if readErr != nil {
			scan.err = readErr
			return scan
		}
		if len(names) == 0 {
			scan.complete = true
			return scan
		}
		for _, name := range names {
			switch {
			case resolvercatalog.IsRetentionRootArtifactName(name):
				info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
				if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					if infoErr == nil {
						infoErr = fmt.Errorf("resolver retention artifact %q is not regular", name)
					}
					scan.err = infoErr
					return scan
				}
				if err := scan.selectIdentity(request, name, info.Size()); errors.Is(err, io.EOF) {
					return scan
				} else if err != nil {
					scan.err = err
					return scan
				}
			case resolvercatalog.IsRetentionStageName(name):
				info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
				if infoErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					if infoErr == nil {
						infoErr = fmt.Errorf("resolver retention stage %q is not a real directory", name)
					}
					scan.err = infoErr
					return scan
				}
				stageScan := scanResolverStage(
					ctx, directory, name, info, request, &scan,
				)
				if stageScan != nil {
					scan.err = stageScan
					return scan
				}
				if scan.truncated {
					return scan
				}
			}
		}
	}
}

func scanResolverStage(
	ctx context.Context,
	parent *stableDirectory,
	name string,
	validated os.FileInfo,
	request store.RetentionComponentRequest,
	scan *fileScan,
) (resultErr error) {
	directory, err := parent.openChildDirectory(ctx, name, validated)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.close())
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, readErr := directory.readBatch(request.Component)
		if readErr != nil {
			return readErr
		}
		if len(names) == 0 {
			return nil
		}
		for _, name := range names {
			info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
			if infoErr != nil {
				return infoErr
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("resolver stage artifact %q is not regular", name)
			}
			qualified := filepath.Join(filepath.Base(directory.path), name)
			if err := scan.selectIdentity(request, qualified, info.Size()); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return err
			}
		}
	}
}

func scanCallerFiles(
	ctx context.Context,
	root string,
	request store.RetentionComponentRequest,
	budget *collectionBudget,
) (scan fileScan) {
	return scanWithStableRoot(ctx, root, budget, func(anchor *stableRoot) fileScan {
		return scanCallerFilesAt(ctx, anchor, ".", request)
	})
}

type queuedCallerDirectory struct {
	name string
	info os.FileInfo
}

func scanCallerFilesAt(
	ctx context.Context,
	anchor *stableRoot,
	relative string,
	request store.RetentionComponentRequest,
) (scan fileScan) {
	directory, err := openStableDirectoryAt(ctx, anchor, relative, nil)
	if errors.Is(err, os.ErrNotExist) {
		return fileScan{complete: true}
	}
	if err != nil {
		return fileScan{err: err}
	}
	defer func() {
		if closeErr := directory.close(); closeErr != nil {
			scan.complete = false
			scan.err = errors.Join(scan.err, closeErr)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			scan.err = err
			return scan
		}
		names, readErr := directory.readBatch(request.Component)
		if readErr != nil {
			scan.err = readErr
			return scan
		}
		if len(names) == 0 {
			scan.complete = true
			return scan
		}
		queue := make(
			[]queuedCallerDirectory, 0,
			min(len(names), directory.budget.policy.queuedCallerDirs),
		)
		for _, name := range names {
			if !callerpublication.IsRetentionRepositoryDirectoryName(name) {
				continue
			}
			info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
			if infoErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				if infoErr == nil {
					infoErr = fmt.Errorf("caller retention repository %q is not a real directory", name)
				}
				scan.err = infoErr
				return scan
			}
			queue = append(queue, queuedCallerDirectory{name: name, info: info})
			if len(queue) == directory.budget.policy.queuedCallerDirs {
				if scanErr := scanCallerQueue(ctx, directory, queue, request, &scan); scanErr != nil {
					scan.err = scanErr
					return scan
				}
				queue = queue[:0]
				if scan.truncated {
					return scan
				}
			}
		}
		if scanErr := scanCallerQueue(ctx, directory, queue, request, &scan); scanErr != nil {
			scan.err = scanErr
			return scan
		}
		if scan.truncated {
			return scan
		}
	}
}

func scanCallerQueue(
	ctx context.Context,
	parent *stableDirectory,
	queue []queuedCallerDirectory,
	request store.RetentionComponentRequest,
	scan *fileScan,
) error {
	for _, queued := range queue {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory, err := parent.openChildDirectory(ctx, queued.name, queued.info)
		if err != nil {
			return err
		}
		repositoryDirectory := queued.name
		for {
			if err := ctx.Err(); err != nil {
				return errors.Join(err, directory.close())
			}
			names, readErr := directory.readBatch(request.Component)
			if readErr != nil {
				return errors.Join(readErr, directory.close())
			}
			if len(names) == 0 {
				break
			}
			for _, name := range names {
				switch {
				case callerpublication.IsRetentionArtifactName(name):
					info, infoErr := stableEntryInfo(ctx, directory, name, directory.budget)
					if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
						if infoErr == nil {
							infoErr = fmt.Errorf("caller retention artifact %q is not regular", name)
						}
						return errors.Join(infoErr, directory.close())
					}
					qualified := filepath.Join(repositoryDirectory, name)
					if err := scan.selectIdentity(request, qualified, info.Size()); errors.Is(err, io.EOF) {
						return directory.close()
					} else if err != nil {
						return errors.Join(err, directory.close())
					}
				case callerpublication.IsRetentionIgnoredControlName(name):
					continue
				}
			}
		}
		if err := directory.close(); err != nil {
			return err
		}
		if scan.truncated {
			return nil
		}
	}
	return nil
}
