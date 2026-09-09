package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/format"
	"io"
	"os"
	"path/filepath"
)

const zoektOfferSourceSHA256 = "sha256:5862b33dc67ce468b50fb81938c8b63abe7602dbf7945202eda0034d9a60954d"
const zoektOfferSourceBytes = 40449
const zoektOfferBuildRecipe = "go-build-trimpath-exact-module-graph-private-versioned-replacement-overlay-v2:github.com/sourcegraph/zoekt/cmd/zoekt-git-index;exact-pinned-module-copy;v3.mod-version-qualified-replace-to-./zoekt;gitindex/index.go;native-go-git-input-offers-v1"
const zoektOfferProvenance = "go-module-private-replacement-overlay-build-v2"

const zoektOfferEntry = `
	t422Offer, t422Finish, t422Err := t422IndexOffers(opts)
	if t422Err != nil { return false, t422Err }
	defer func() { if finishErr := t422Finish(); finishErr != nil { retErr = errors.Join(retErr, finishErr) } }()
`

const zoektOfferCall = `
		if err := t422Offer(); err != nil { return false, err }
`

// The injected hook uses only imports already present in the pinned Apache-2.0
// source. It shares the standard logger's native file object without changing
// logger configuration. A token is written before the actual go-git offer.
const zoektOfferHook = `

// t422IndexOffers is the Phebs V3-only native input-offer build instrumentation.
func t422IndexOffers(opts Options) (func() error, func() error, error) {
	noop := func() error { return nil }
	mode := os.Getenv("PHEBS_T422_INDEX_OFFERS")
	if mode == "" { return noop, noop, nil }
	if mode != "v1" || os.Getenv("ZOEKT_DISABLE_CATFILE_BATCH") != "true" || opts.Submodules || opts.Incremental {
		return nil, nil, fmt.Errorf("T422 index offer mode unavailable")
	}
	writer, ok := log.Writer().(*os.File)
	if !ok || writer != os.Stderr { return nil, nil, fmt.Errorf("T422 index offer writer unavailable") }
	write := func(value string) error {
		n, err := writer.WriteString(value)
		if err != nil { return err }
		if n != len(value) { return io.ErrShortWrite }
		return nil
	}
	if err := write("ZIB1\n"); err != nil { return nil, nil, err }
	var count uint64
	offer := func() error {
		if count == ^uint64(0) { return fmt.Errorf("T422 index offer overflow") }
		if err := write("ZI1\n"); err != nil { return err }
		count++
		return nil
	}
	finish := func() error { return write("ZIE1:" + strconv.FormatUint(count, 10) + "\n") }
	return offer, finish, nil
}
`

func zoektOfferRecipe(policy ToolPolicy, sourceCommit string) string {
	return recipeDigest("t422-zoekt-overlay-build-recipe-v1", policy.ZoektModulePath,
		policy.ZoektModuleVersion, policy.ZoektModuleSum, policy.ZoektBuildRecipe,
		sourceCommit, zoektOfferSourceSHA256,
		SHA256([]byte(zoektOfferEntry+zoektOfferCall+zoektOfferHook)))
}

func transformZoektOffers(source []byte) ([]byte, error) {
	if SHA256(source) != zoektOfferSourceSHA256 {
		return nil, errors.New("zoekt offer overlay source differs from the pinned file")
	}
	for _, edit := range []struct{ before, after string }{
		{"func indexGitRepo(opts Options, config gitIndexConfig) (bool, error) {", "func indexGitRepo(opts Options, config gitIndexConfig) (updated bool, retErr error) {" + zoektOfferEntry},
		{"\tfor idx, key := range submoduleKeys {\n", "\tfor idx, key := range submoduleKeys {\n" + zoektOfferCall},
	} {
		if bytes.Count(source, []byte(edit.before)) != 1 {
			return nil, errors.New("zoekt offer overlay anchor is not unique")
		}
		source = bytes.Replace(source, []byte(edit.before), []byte(edit.after), 1)
	}
	return format.Source(append(source, []byte(zoektOfferHook)...))
}

// Prepared only inside fresh, private build scratch. Protected source/modules
// stay byte-exact; V3 alone derives a scratch-local versioned replacement.
func referenceToolBuildArgs(ctx context.Context, role, schema, sourceRoot, moduleCache, workspace, output, packagePath string) (string, []string, func() error, error) {
	args := []string{"build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1"}
	check := func() error { return nil }
	if role == "zoekt-git-index" && schema == PlanV3Schema {
		directory, path, verify, err := prepareZoektOfferBuild(ctx, sourceRoot, moduleCache, workspace)
		if err != nil {
			return "", nil, nil, err
		}
		args, check = append(args, "-modfile=v3.mod", "-overlay="+path), verify
		sourceRoot = directory
	}
	return sourceRoot, append(args, "-o", output, packagePath), check, nil
}

func prepareZoektOfferOverlay(moduleDirectory, workspace string) (string, func() error, error) {
	original := filepath.Join(moduleDirectory, "gitindex", "index.go")
	source, err := readZoektOverlayFile(original, zoektOfferSourceBytes)
	if err != nil {
		return "", nil, err
	}
	patched, err := transformZoektOffers(source)
	if err != nil {
		return "", nil, err
	}
	directory, err := os.MkdirTemp(workspace, "zoekt-offers-")
	if err != nil {
		return "", nil, err
	}
	replacement := filepath.Join(directory, "index.go")
	if err := os.WriteFile(replacement, patched, 0o400); err != nil {
		return "", nil, err
	}
	raw, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: replacement}})
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(path, raw, 0o400); err != nil {
		return "", nil, err
	}
	checks := []struct {
		path, digest string
		size         int
	}{
		{original, zoektOfferSourceSHA256, len(source)}, {replacement, SHA256(patched), len(patched)}, {path, SHA256(raw), len(raw)},
	}
	return path, func() error {
		for _, check := range checks {
			content, err := readZoektOverlayFile(check.path, check.size)
			if err != nil || SHA256(content) != check.digest {
				return errors.New("zoekt offer overlay input changed")
			}
		}
		return nil
	}, nil
}

func readZoektOverlayFile(path string, size int) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || size < 1 || size > 64<<10 {
		return nil, errors.New("zoekt offer overlay file shape is invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() != int64(size) {
		return nil, errors.New("zoekt offer overlay file size or type differs")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("zoekt offer overlay file changed before read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(size)+1))
	if err != nil || len(raw) != size {
		return nil, errors.New("zoekt offer overlay file length changed")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() != int64(size) {
		return nil, errors.New("zoekt offer overlay file changed during read")
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return raw, nil
}
