package t4110

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxComposedTreeEntries = 100_000
	maxComposedTreeBytes   = int64(2 << 30)
	maxComposedFileBytes   = int64(256 << 20)
	composedExecutionDir   = ".t4110-execution"
)

type composedToolchain struct {
	git     admittedExecutable
	goTool  admittedExecutable
	node    admittedExecutable
	npm     admittedExecutable
	surreal admittedExecutable
}

func (tools composedToolchain) verify() error {
	for _, tool := range []struct {
		name string
		admittedExecutable
	}{
		{name: "git", admittedExecutable: tools.git},
		{name: "go", admittedExecutable: tools.goTool},
		{name: "node", admittedExecutable: tools.node},
		{name: "npm", admittedExecutable: tools.npm},
		{name: "surreal", admittedExecutable: tools.surreal},
	} {
		if err := tool.verify(); err != nil {
			return fmt.Errorf("verify composed %s executable: %w", tool.name, err)
		}
	}
	return nil
}

func exportComposedTree(
	ctx context.Context,
	repositoryRoot string,
	git admittedExecutable,
) (_ string, retErr error) {
	if err := git.verify(); err != nil {
		return "", err
	}
	destination, err := os.MkdirTemp("", "phebs-t4110-composed-")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			retErr = errors.Join(retErr, removeComposedTree(destination))
		}
	}()
	entries, err := readHeadTree(ctx, repositoryRoot, git.path)
	if err != nil {
		return "", err
	}
	requests := make([]byte, 0, len(entries)*65)
	for _, entry := range entries {
		requests = append(requests, entry.object...)
		requests = append(requests, '\n')
	}
	command := exec.CommandContext(
		ctx,
		git.path,
		"-C",
		repositoryRoot,
		"cat-file",
		"--batch",
	)
	command.Env = gitEnvironment()
	command.Stdin = bytes.NewReader(requests)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return "", err
	}
	extractErr := extractGitBlobs(destination, entries, stdout)
	if extractErr != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if extractErr != nil || waitErr != nil {
		return "", errors.Join(
			extractErr,
			waitErr,
			boundedCommandError(stderr.String()),
		)
	}
	keep = true
	return destination, nil
}

type gitTreeEntry struct {
	object string
	path   string
	mode   fs.FileMode
}

func readHeadTree(
	ctx context.Context,
	repositoryRoot, gitBinary string,
) ([]gitTreeEntry, error) {
	output, err := runCommand(
		ctx, repositoryRoot, gitBinary, "ls-tree", "-rz", "--full-tree", "HEAD",
	)
	if err != nil {
		return nil, fmt.Errorf("read exact HEAD tree: %w", err)
	}
	if len(output) > 64<<20 {
		return nil, errors.New("exact HEAD tree inventory exceeds its closed bound")
	}
	records := strings.Split(output, "\x00")
	if records[len(records)-1] != "" {
		return nil, errors.New("exact HEAD tree inventory is truncated")
	}
	records = records[:len(records)-1]
	if len(records) == 0 || len(records) > maxComposedTreeEntries {
		return nil, errors.New("exact HEAD tree inventory exceeds its closed bound")
	}
	entries := make([]gitTreeEntry, 0, len(records))
	prior := ""
	for _, record := range records {
		header, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || fields[1] != "blob" ||
			(fields[0] != "100644" && fields[0] != "100755") ||
			!validObjectID(fields[2]) || !fs.ValidPath(path) ||
			strings.ContainsRune(path, '\\') || path <= prior {
			return nil, errors.New("exact HEAD tree contains an unsupported entry")
		}
		mode := fs.FileMode(0o644)
		if fields[0] == "100755" {
			mode = 0o755
		}
		entries = append(entries, gitTreeEntry{object: fields[2], path: path, mode: mode})
		prior = path
	}
	return entries, nil
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func verifyCheckoutMatchesExport(
	ctx context.Context,
	repositoryRoot, exportedRoot string,
	git admittedExecutable,
) error {
	if err := git.verify(); err != nil {
		return err
	}
	entries, err := readHeadTree(ctx, repositoryRoot, git.path)
	if err != nil {
		return err
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve authoring checkout: %w", err)
	}
	exportedRoot, err = filepath.EvalSymlinks(exportedRoot)
	if err != nil {
		return fmt.Errorf("resolve exact HEAD export: %w", err)
	}
	checkoutDirectories := make(map[string]bool)
	exportDirectories := make(map[string]bool)
	for _, entry := range entries {
		if err := verifyRegularPath(
			repositoryRoot, entry.path, entry.mode, checkoutDirectories,
		); err != nil {
			return fmt.Errorf("authoring checkout differs from exact HEAD export: %w", err)
		}
		if err := verifyRegularPath(
			exportedRoot, entry.path, entry.mode, exportDirectories,
		); err != nil {
			return fmt.Errorf("exact HEAD export differs from its inventory: %w", err)
		}
		equal, err := regularFilesEqual(
			filepath.Join(repositoryRoot, filepath.FromSlash(entry.path)),
			filepath.Join(exportedRoot, filepath.FromSlash(entry.path)),
		)
		if err != nil {
			return fmt.Errorf("compare authoring checkout with exact HEAD export: %w", err)
		}
		if !equal {
			return errors.New("authoring checkout bytes differ from exact HEAD export")
		}
	}
	return nil
}

func bindComposedTreeGit(
	ctx context.Context,
	repositoryRoot, exportedRoot, commit string,
	git admittedExecutable,
) error {
	if err := git.verify(); err != nil {
		return err
	}
	if !validCommit(commit) {
		return errors.New("exact composed Git commit is invalid")
	}
	if _, err := os.Lstat(filepath.Join(exportedRoot, ".git")); !os.IsNotExist(err) {
		return errors.Join(err, errors.New("exact composed tree already has Git metadata"))
	}
	for _, arguments := range [][]string{
		{"init", "--quiet", "--object-format=sha1"},
		{"fetch", "--quiet", "--no-tags", "--depth=1", repositoryRoot, commit},
		{"update-ref", "refs/heads/t4110-exact", commit},
		{"symbolic-ref", "HEAD", "refs/heads/t4110-exact"},
		{"read-tree", "HEAD"},
	} {
		if _, err := runCommand(ctx, exportedRoot, git.path, arguments...); err != nil {
			return fmt.Errorf("bind exact composed Git metadata: %w", err)
		}
	}
	exclude := filepath.Join(exportedRoot, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("/.t4110-execution/\n"), 0o600); err != nil {
		return fmt.Errorf("exclude exact composed execution custody: %w", err)
	}
	boundCommit, err := verifyCleanCommitWithGit(ctx, exportedRoot, git.path)
	if err != nil || boundCommit != commit {
		return errors.Join(errors.New("exact composed tree is not bound to clean HEAD"), err)
	}
	return verifyCheckoutMatchesExport(ctx, repositoryRoot, exportedRoot, git)
}

func verifyRegularPath(
	root, relative string,
	wantMode fs.FileMode,
	checkedDirectories map[string]bool,
) error {
	current := root
	components := strings.Split(relative, "/")
	for _, component := range components[:len(components)-1] {
		current = filepath.Join(current, component)
		if checkedDirectories != nil && checkedDirectories[current] {
			continue
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(err, errors.New("tracked path has a non-directory ancestor"))
		}
		if checkedDirectories != nil {
			checkedDirectories[current] = true
		}
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o111 != wantMode.Perm()&0o111 {
		return errors.Join(err, errors.New("tracked path type or executable mode differs"))
	}
	return nil
}

func regularFilesEqual(leftPath, rightPath string) (_ bool, retErr error) {
	left, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer func() { retErr = errors.Join(retErr, left.Close()) }()
	right, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer func() { retErr = errors.Join(retErr, right.Close()) }()
	leftInfo, err := left.Stat()
	if err != nil {
		return false, err
	}
	rightInfo, err := right.Stat()
	if err != nil || leftInfo.Size() != rightInfo.Size() {
		return false, err
	}
	leftDigest, rightDigest := sha256.New(), sha256.New()
	if _, err := io.Copy(leftDigest, left); err != nil {
		return false, err
	}
	if _, err := io.Copy(rightDigest, right); err != nil {
		return false, err
	}
	return bytes.Equal(leftDigest.Sum(nil), rightDigest.Sum(nil)), nil
}

func extractGitBlobs(
	destination string,
	entries []gitTreeEntry,
	reader io.Reader,
) error {
	buffered := bufio.NewReader(reader)
	var total int64
	for _, entry := range entries {
		header, err := buffered.ReadString('\n')
		fields := strings.Fields(header)
		if err != nil || len(fields) != 3 || fields[0] != entry.object || fields[1] != "blob" {
			return errors.Join(err, errors.New("exact HEAD blob header is invalid"))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > maxComposedFileBytes || size > maxComposedTreeBytes-total {
			return errors.New("exact HEAD blob exceeds its closed bound")
		}
		total += size
		path := filepath.Join(destination, filepath.FromSlash(entry.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, entry.mode)
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(file, buffered, size)
		closeErr := file.Close()
		separator, separatorErr := buffered.ReadByte()
		if copyErr != nil || closeErr != nil || separatorErr != nil || written != size || separator != '\n' {
			return errors.Join(
				copyErr, closeErr, separatorErr,
				errors.New("exact HEAD blob is incomplete"),
			)
		}
	}
	if extra, err := buffered.ReadByte(); !errors.Is(err, io.EOF) {
		return errors.Join(err, fmt.Errorf("unexpected trailing exact HEAD byte %d", extra))
	}
	return nil
}

func removeComposedTree(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.Join(errors.New("composed source custody remained after cleanup"), err)
	}
	return nil
}

func prepareComposedEnvironment(
	repositoryRoot string,
	surreal admittedExecutable,
) error {
	root := filepath.Join(repositoryRoot, composedExecutionDir)
	if err := os.Mkdir(root, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"home", "tmp", "go-cache", "npm-prefix", "bin"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			return err
		}
	}
	for _, name := range []string{"npm-userconfig", "npm-globalconfig"} {
		file, err := os.OpenFile(
			filepath.Join(root, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
		)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return os.Symlink(surreal.path, filepath.Join(root, "bin", "surreal"))
}

func composedEnvironment(
	tools composedToolchain,
	repositoryRoot string,
	goCommand bool,
) []string {
	hostHome, _ := os.UserHomeDir()
	root := filepath.Join(repositoryRoot, composedExecutionDir)
	home := filepath.Join(root, "home")
	temporary := filepath.Join(root, "tmp")
	paths := []string{
		filepath.Dir(tools.goTool.path),
		filepath.Dir(tools.node.path),
		filepath.Dir(tools.git.path),
		filepath.Join(root, "bin"),
		"/usr/bin",
		"/bin",
	}
	result := []string{
		"HOME=" + home,
		"TMPDIR=" + temporary,
		"PATH=" + strings.Join(paths, ":"),
		"LANG=C",
		"LC_ALL=C",
		"TZ=UTC",
		"NO_COLOR=1",
		"PHEBS_SURREAL=" + tools.surreal.path,
		"PHEBS_SURREAL_SHA256=" + tools.surreal.sha256,
	}
	if goCommand {
		return append(result,
			"CGO_ENABLED=0",
			"GOCACHE="+filepath.Join(root, "go-cache"),
			"GOENV=off",
			"GOEXPERIMENT=",
			"GOFLAGS=-mod=readonly",
			"GOFIPS140=off",
			"GOMODCACHE="+filepath.Join(hostHome, "go", "pkg", "mod"),
			"GOPATH="+filepath.Join(home, "go"),
			"GOWORK=off",
			"GOTOOLCHAIN=local",
			"GOPROXY=off",
			"GOSUMDB=off",
			"GOTELEMETRY=off",
		)
	}
	return append(result,
		"CI=1",
		"npm_config_audit=false",
		"npm_config_cache="+filepath.Join(hostHome, ".npm"),
		"npm_config_fund=false",
		"npm_config_globalconfig="+filepath.Join(root, "npm-globalconfig"),
		"npm_config_ignore_scripts=true",
		"npm_config_offline=true",
		"npm_config_prefix="+filepath.Join(root, "npm-prefix"),
		"npm_config_update_notifier=false",
		"npm_config_userconfig="+filepath.Join(root, "npm-userconfig"),
	)
}
