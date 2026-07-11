package sync

// Revision-pinned Git history reads over managed bare mirrors (Epic 9).
// Every public operation resolves mutable refs once, then uses only immutable
// commit/blob OIDs for the remainder of the request.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	defaultCommitLimit             = 50
	defaultMaxCommitLimit          = 200
	defaultMaxCommitOffset         = 10_000
	defaultMaxBlameLines           = 50_000
	defaultMaxBlameBlobBytes       = 10 << 20
	defaultMaxHistoryOutput        = 64 << 20
	defaultMaxDiffBytes            = 2 << 20
	defaultDiffContextLines        = 3
	defaultMaxContextLines         = 20
	historyStderrLimit       int64 = 64 << 10
)

// ErrBinaryFile reports a file for which line-oriented history is undefined.
var (
	ErrBinaryFile  = errors.New("binary file")
	errOutputLimit = errors.New("git output limit reached")
)

// HistoryService serves read-only Git history from the mirrors in DataDir.
// Zero-valued limits use conservative defaults. Limit fields are exported so
// deployments and tests can lower bounds without process-wide mutable state.
type HistoryService struct {
	DataDir               string
	MaxCommitLimit        int
	MaxBlameLines         int
	MaxBlameBlobBytes     int64
	MaxHistoryOutputBytes int64
	MaxDiffBytes          int64
}

func NewHistoryService(dataDir string) *HistoryService {
	return &HistoryService{DataDir: dataDir}
}

type GitIdentity struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Time  time.Time `json:"time"`
}

type GitCommit struct {
	ID        string      `json:"id"`
	ShortID   string      `json:"short_id"`
	ParentIDs []string    `json:"parent_ids"`
	Subject   string      `json:"subject"`
	Message   string      `json:"message"`
	Author    GitIdentity `json:"author"`
	Committer GitIdentity `json:"committer"`
}

type BlameRequest struct {
	Repo string
	Ref  string
	Path string
}

type BlameLine struct {
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
	CommitID     string `json:"commit_id"`
	OriginalPath string `json:"original_path"`
	Content      string `json:"content"`
}

type BlameResult struct {
	Revision  string      `json:"revision"`
	Path      string      `json:"path"`
	Lines     []BlameLine `json:"lines"`
	Commits   []GitCommit `json:"commits"`
	Truncated bool        `json:"truncated"`
}

type CommitListRequest struct {
	Repo   string
	Ref    string
	Path   string
	Limit  int
	Offset int
}

type CommitListResult struct {
	Revision string      `json:"revision"`
	Commits  []GitCommit `json:"commits"`
	Offset   int         `json:"offset"`
	HasMore  bool        `json:"has_more"`
}

type CommitRequest struct {
	Repo string
	Ref  string
}

type GitFileChange struct {
	Status     string `json:"status"`
	Path       string `json:"path"`
	OldPath    string `json:"old_path,omitempty"`
	Similarity int    `json:"similarity,omitempty"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
	Binary     bool   `json:"binary,omitempty"`
}

type CommitResult struct {
	Revision string          `json:"revision"`
	Commit   GitCommit       `json:"commit"`
	Changes  []GitFileChange `json:"changes"`
}

type DiffRequest struct {
	Repo            string
	Base            string
	Head            string
	Path            string
	ContextLines    int
	ContextLinesSet bool
}

type DiffResult struct {
	Base      string          `json:"base,omitempty"`
	Head      string          `json:"head"`
	Patch     string          `json:"patch"`
	Files     []GitFileChange `json:"files"`
	Truncated bool            `json:"truncated"`
}

func (s *HistoryService) maxCommitLimit() int {
	if s != nil && s.MaxCommitLimit > 0 {
		return s.MaxCommitLimit
	}
	return defaultMaxCommitLimit
}

func (s *HistoryService) maxBlameLines() int {
	if s != nil && s.MaxBlameLines > 0 {
		return s.MaxBlameLines
	}
	return defaultMaxBlameLines
}

func (s *HistoryService) maxBlameBlobBytes() int64 {
	if s != nil && s.MaxBlameBlobBytes > 0 {
		return s.MaxBlameBlobBytes
	}
	return defaultMaxBlameBlobBytes
}

func (s *HistoryService) maxHistoryOutput() int64 {
	if s != nil && s.MaxHistoryOutputBytes > 0 {
		return s.MaxHistoryOutputBytes
	}
	return defaultMaxHistoryOutput
}

func (s *HistoryService) maxDiffOutput() int64 {
	if s != nil && s.MaxDiffBytes > 0 {
		return s.MaxDiffBytes
	}
	return defaultMaxDiffBytes
}

func (s *HistoryService) repoDir(repo string) (string, error) {
	if s == nil {
		return "", errors.New("nil history service")
	}
	return SafeRepoDir(s.DataDir, repo)
}

// Blame returns line attribution for Path at a single resolved commit. Git's
// move/copy detection retains original paths and line numbers across renames.
func (s *HistoryService) Blame(ctx context.Context, req BlameRequest) (BlameResult, error) {
	var result BlameResult
	dir, dirErr := s.repoDir(req.Repo)
	if err := errors.Join(dirErr, checkRef(req.Ref), checkPath(req.Path)); err != nil {
		return result, err
	}
	if req.Path == "" {
		return result, fmt.Errorf("empty path: %w", ErrBadInput)
	}
	oid, err := resolveCommit(ctx, dir, req.Ref)
	if err != nil {
		return result, err
	}
	result.Revision, result.Path = oid, req.Path

	blob, size, err := resolveBlob(ctx, dir, oid, req.Path)
	if err != nil {
		return result, err
	}
	if size == 0 {
		result.Lines, result.Commits = []BlameLine{}, []GitCommit{}
		return result, nil
	}
	if limit := s.maxBlameBlobBytes(); size > limit {
		return result, fmt.Errorf("%s is %d bytes (blame limit %d): %w", req.Path, size, limit, ErrTooLarge)
	}

	lineCap := s.maxBlameLines()
	args := []string{
		"blame", "--line-porcelain", "--root", "-M", "-C",
		"-L", fmt.Sprintf("1,%d", lineCap+1), oid, "--", req.Path,
	}
	out, truncatedOutput, err := runGitLimited(ctx, dir, s.maxHistoryOutput(), args...)
	if err != nil {
		return result, err
	}
	if truncatedOutput {
		return result, fmt.Errorf("blame output exceeds %d bytes: %w", s.maxHistoryOutput(), ErrTooLarge)
	}
	if bytes.IndexByte(out, 0) >= 0 {
		return result, fmt.Errorf("%s at %s (%s): %w", req.Path, oid, blob, ErrBinaryFile)
	}

	lines, commitOrder, err := parseBlame(out)
	if err != nil {
		return result, err
	}
	if len(lines) > lineCap {
		lines = lines[:lineCap]
		result.Truncated = true
	}
	commits, err := loadCommits(ctx, dir, commitOrder, s.maxHistoryOutput())
	if err != nil {
		return result, err
	}
	result.Lines, result.Commits = lines, commits
	return result, nil
}

// Commits lists commits reachable from the resolved revision. With Path set,
// --follow makes the list track a file through renames.
func (s *HistoryService) Commits(ctx context.Context, req CommitListRequest) (CommitListResult, error) {
	var result CommitListResult
	dir, dirErr := s.repoDir(req.Repo)
	if err := errors.Join(dirErr, checkRef(req.Ref), checkPath(req.Path)); err != nil {
		return result, err
	}
	if req.Offset < 0 || req.Offset > defaultMaxCommitOffset || req.Limit < 0 {
		return result, fmt.Errorf("commit pagination is out of range: %w", ErrBadInput)
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultCommitLimit
	}
	if limit > s.maxCommitLimit() {
		limit = s.maxCommitLimit()
	}
	oid, err := resolveCommit(ctx, dir, req.Ref)
	if err != nil {
		return result, err
	}
	result.Revision, result.Offset = oid, req.Offset

	args := []string{
		"log", "--no-show-signature", "-z", "--format=" + commitFormat,
		// Git applies --skip before history path simplification, so it cannot
		// paginate --follow reliably. Read the bounded prefix and slice the
		// already path-filtered records instead.
		"--max-count=" + strconv.Itoa(req.Offset+limit+1),
	}
	if req.Path != "" {
		args = append(args, "--follow")
	}
	args = append(args, oid)
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}
	out, truncated, err := runGitLimited(ctx, dir, s.maxHistoryOutput(), args...)
	if err != nil {
		return result, err
	}
	if truncated {
		return result, fmt.Errorf("commit list exceeds %d bytes: %w", s.maxHistoryOutput(), ErrTooLarge)
	}
	commits, err := parseCommitRecords(out)
	if err != nil {
		return result, err
	}
	if len(commits) > req.Offset+limit {
		result.HasMore = true
	}
	if req.Offset >= len(commits) {
		result.Commits = []GitCommit{}
		return result, nil
	}
	commits = commits[req.Offset:]
	if len(commits) > limit {
		commits = commits[:limit]
	}
	result.Commits = commits
	return result, nil
}

// Commit returns metadata and first-parent file changes for one resolved
// commit. Root commits are compared with the empty tree.
func (s *HistoryService) Commit(ctx context.Context, req CommitRequest) (CommitResult, error) {
	var result CommitResult
	dir, dirErr := s.repoDir(req.Repo)
	if err := errors.Join(dirErr, checkRef(req.Ref)); err != nil {
		return result, err
	}
	oid, err := resolveCommit(ctx, dir, req.Ref)
	if err != nil {
		return result, err
	}
	commits, err := loadCommits(ctx, dir, []string{oid}, s.maxHistoryOutput())
	if err != nil {
		return result, err
	}
	if len(commits) != 1 {
		return result, fmt.Errorf("resolved commit %s disappeared", oid)
	}
	changes, err := loadChanges(ctx, dir, firstParent(commits[0]), oid, "", s.maxHistoryOutput())
	if err != nil {
		return result, err
	}
	result.Revision, result.Commit, result.Changes = oid, commits[0], changes
	return result, nil
}

// Diff returns a bounded unified patch and structured file statistics. If
// Base is empty, Head is compared with its first parent (or the empty tree for
// a root commit). Binary content is never embedded in Patch.
func (s *HistoryService) Diff(ctx context.Context, req DiffRequest) (DiffResult, error) {
	var result DiffResult
	dir, dirErr := s.repoDir(req.Repo)
	if err := errors.Join(dirErr, checkRef(req.Base), checkRef(req.Head), checkPath(req.Path)); err != nil {
		return result, err
	}
	contextLines := req.ContextLines
	if !req.ContextLinesSet && contextLines == 0 {
		contextLines = defaultDiffContextLines
	}
	if contextLines < 0 || contextLines > defaultMaxContextLines {
		return result, fmt.Errorf("context lines must be between 0 and %d: %w", defaultMaxContextLines, ErrBadInput)
	}
	head, err := resolveCommit(ctx, dir, req.Head)
	if err != nil {
		return result, err
	}
	result.Head = head
	base := ""
	if req.Base != "" {
		base, err = resolveCommit(ctx, dir, req.Base)
		if err != nil {
			return result, err
		}
	} else {
		commits, loadErr := loadCommits(ctx, dir, []string{head}, s.maxHistoryOutput())
		if loadErr != nil {
			return result, loadErr
		}
		if len(commits) != 1 {
			return result, fmt.Errorf("resolved commit %s disappeared", head)
		}
		base = firstParent(commits[0])
	}
	result.Base = base

	files, err := loadChanges(ctx, dir, base, head, req.Path, s.maxHistoryOutput())
	if err != nil {
		return result, err
	}
	args := diffCommand(base, head,
		"--patch", "--no-ext-diff", "--no-textconv", "--no-color",
		"--find-renames", "--unified="+strconv.Itoa(contextLines),
	)
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}
	patch, truncated, err := runGitLimited(ctx, dir, s.maxDiffOutput(), args...)
	if err != nil {
		return result, err
	}
	result.Files = files
	// A one-byte replacement keeps the returned UTF-8 string within the same
	// byte cap even when a repository contains malformed path/text bytes.
	result.Patch = strings.ToValidUTF8(string(patch), "?")
	result.Truncated = truncated
	return result, nil
}

func resolveCommit(ctx context.Context, dir, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	out, err := runGitRaw(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !isObjectID(oid) {
		return "", fmt.Errorf("git returned invalid commit id %q", oid)
	}
	return oid, nil
}

func resolveBlob(ctx context.Context, dir, commit, path string) (string, int64, error) {
	out, err := runGitRaw(ctx, dir, "rev-parse", "--verify", spec(commit, path))
	if err != nil {
		return "", 0, err
	}
	oid := strings.TrimSpace(string(out))
	typeOut, err := runGitRaw(ctx, dir, "cat-file", "-t", oid)
	if err != nil || strings.TrimSpace(string(typeOut)) != "blob" {
		return "", 0, fmt.Errorf("%s is not a blob: %w", path, store.ErrNotFound)
	}
	sizeOut, err := runGitRaw(ctx, dir, "cat-file", "-s", oid)
	if err != nil {
		return "", 0, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("parse blob size: %w", err)
	}
	return oid, size, nil
}

func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type limitedBuffer struct {
	buf        bytes.Buffer
	limit      int64
	truncated  bool
	onOverflow func()
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - int64(w.buf.Len())
	if int64(len(p)) <= remaining {
		return w.buf.Write(p)
	}
	keep := 0
	if remaining > 0 {
		keep = int(remaining)
		_, _ = w.buf.Write(p[:keep])
	}
	w.truncated = true
	if w.onOverflow != nil {
		w.onOverflow()
		return keep, errOutputLimit
	}
	// Stderr remains diagnostic-only: retain its prefix without letting a
	// noisy failing process allocate unbounded memory.
	return len(p), nil
}

func runGitLimited(ctx context.Context, dir string, limit int64, args ...string) ([]byte, bool, error) {
	// --literal-pathspecs prevents a repository path such as ":(exclude)*"
	// from being interpreted as Git pathspec magic and widening the request.
	full := append([]string{"-c", "core.quotePath=false", "--literal-pathspecs"}, args...)
	commandCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(commandCtx, "git", full...)
	cmd.Dir = dir
	stdout := &limitedBuffer{limit: limit, onOverflow: stop}
	stderr := &limitedBuffer{limit: historyStderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		// Caller cancellation/deadline wins over an internal output-limit kill.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		if stdout.truncated {
			return stdout.buf.Bytes(), true, nil
		}
		return nil, false, gitCommandError(ctx, err, args, stderr.buf.String())
	}
	return stdout.buf.Bytes(), stdout.truncated, nil
}

func parseBlameHeader(value string) []string {
	// Blame headers have a fixed, simple grammar; parsing without a process-wide
	// regexp also avoids accepting abbreviated object IDs.
	fields := strings.Fields(value)
	if len(fields) != 3 && len(fields) != 4 {
		return nil
	}
	if !isObjectID(fields[0]) {
		return nil
	}
	if _, err := strconv.Atoi(fields[1]); err != nil {
		return nil
	}
	if _, err := strconv.Atoi(fields[2]); err != nil {
		return nil
	}
	if len(fields) == 4 {
		if _, err := strconv.Atoi(fields[3]); err != nil {
			return nil
		}
	}
	return []string{value, fields[0], fields[1], fields[2]}
}

func parseBlame(out []byte) ([]BlameLine, []string, error) {
	rows := bytes.Split(out, []byte{'\n'})
	lines := make([]BlameLine, 0, len(rows)/8)
	seen := map[string]bool{}
	order := []string{}
	for i := 0; i < len(rows); {
		if len(rows[i]) == 0 {
			i++
			continue
		}
		header := parseBlameHeader(string(rows[i]))
		if header == nil {
			return nil, nil, fmt.Errorf("unexpected blame header %q", rows[i])
		}
		originalLine, _ := strconv.Atoi(header[2])
		finalLine, _ := strconv.Atoi(header[3])
		line := BlameLine{CommitID: header[1], OriginalLine: originalLine, Line: finalLine}
		i++
		foundContent := false
		for i < len(rows) {
			row := string(rows[i])
			i++
			if strings.HasPrefix(row, "filename ") {
				line.OriginalPath = strings.ToValidUTF8(strings.TrimPrefix(row, "filename "), "?")
				continue
			}
			if strings.HasPrefix(row, "\t") {
				line.Content = strings.ToValidUTF8(strings.TrimPrefix(row, "\t"), "?")
				foundContent = true
				break
			}
		}
		if !foundContent {
			return nil, nil, fmt.Errorf("blame record for line %d has no content", finalLine)
		}
		if line.OriginalPath == "" {
			return nil, nil, fmt.Errorf("blame record for line %d has no filename", finalLine)
		}
		lines = append(lines, line)
		if !seen[line.CommitID] {
			seen[line.CommitID] = true
			order = append(order, line.CommitID)
		}
	}
	return lines, order, nil
}

const commitFormat = "%H%x00%h%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%s%x00%B"

func parseCommitRecords(out []byte) ([]GitCommit, error) {
	if len(out) == 0 {
		return []GitCommit{}, nil
	}
	fields := bytes.Split(out, []byte{0})
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	const fieldsPerCommit = 11
	if len(fields)%fieldsPerCommit != 0 {
		return nil, fmt.Errorf("unexpected git log record with %d fields", len(fields))
	}
	commits := make([]GitCommit, 0, len(fields)/fieldsPerCommit)
	for i := 0; i < len(fields); i += fieldsPerCommit {
		authorTime, err := time.Parse(time.RFC3339, string(fields[i+5]))
		if err != nil {
			return nil, fmt.Errorf("parse author time: %w", err)
		}
		committerTime, err := time.Parse(time.RFC3339, string(fields[i+8]))
		if err != nil {
			return nil, fmt.Errorf("parse committer time: %w", err)
		}
		parents := strings.Fields(string(fields[i+2]))
		if parents == nil {
			parents = []string{}
		}
		commits = append(commits, GitCommit{
			ID:        string(fields[i]),
			ShortID:   string(fields[i+1]),
			ParentIDs: parents,
			Subject:   string(fields[i+9]),
			Message:   strings.TrimSuffix(string(fields[i+10]), "\n"),
			Author: GitIdentity{
				Name: string(fields[i+3]), Email: string(fields[i+4]), Time: authorTime,
			},
			Committer: GitIdentity{
				Name: string(fields[i+6]), Email: string(fields[i+7]), Time: committerTime,
			},
		})
	}
	return commits, nil
}

func loadCommits(ctx context.Context, dir string, ids []string, limit int64) ([]GitCommit, error) {
	if len(ids) == 0 {
		return []GitCommit{}, nil
	}
	budget := limit
	remaining := limit
	all := make([]GitCommit, 0, len(ids))
	for start := 0; start < len(ids); start += 128 {
		if remaining <= 0 {
			return nil, fmt.Errorf("commit metadata exceeds %d bytes: %w", budget, ErrTooLarge)
		}
		end := start + 128
		if end > len(ids) {
			end = len(ids)
		}
		args := []string{"show", "-s", "--no-show-signature", "-z", "--format=" + commitFormat}
		args = append(args, ids[start:end]...)
		out, truncated, err := runGitLimited(ctx, dir, remaining, args...)
		if err != nil {
			return nil, err
		}
		if truncated {
			return nil, fmt.Errorf("commit metadata exceeds %d bytes: %w", budget, ErrTooLarge)
		}
		remaining -= int64(len(out))
		batch, err := parseCommitRecords(out)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
	}
	return all, nil
}

func firstParent(commit GitCommit) string {
	if len(commit.ParentIDs) == 0 {
		return ""
	}
	return commit.ParentIDs[0]
}

func diffCommand(base, head string, options ...string) []string {
	if base == "" {
		args := []string{"diff-tree", "--root", "--no-commit-id", "-r"}
		args = append(args, options...)
		return append(args, head)
	}
	args := []string{"diff"}
	args = append(args, options...)
	return append(args, base, head)
}

func loadChanges(ctx context.Context, dir, base, head, path string, limit int64) ([]GitFileChange, error) {
	nameArgs := diffCommand(base, head, "--name-status", "-z", "-M")
	if path != "" {
		nameArgs = append(nameArgs, "--", path)
	}
	nameOut, truncated, err := runGitLimited(ctx, dir, limit, nameArgs...)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("changed-file list exceeds %d bytes: %w", limit, ErrTooLarge)
	}
	changes, err := parseNameStatus(nameOut)
	if err != nil {
		return nil, err
	}

	statArgs := diffCommand(base, head, "--numstat", "-z", "-M")
	if path != "" {
		statArgs = append(statArgs, "--", path)
	}
	statOut, truncated, err := runGitLimited(ctx, dir, limit, statArgs...)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("diff statistics exceed %d bytes: %w", limit, ErrTooLarge)
	}
	stats, err := parseNumstat(statOut)
	if err != nil {
		return nil, err
	}
	for i := range changes {
		key := changeKey(changes[i].OldPath, changes[i].Path)
		if stat, ok := stats[key]; ok {
			changes[i].Additions = stat.Additions
			changes[i].Deletions = stat.Deletions
			changes[i].Binary = stat.Binary
			continue
		}
		// Non-rename records use only Path in numstat.
		if stat, ok := stats[changeKey("", changes[i].Path)]; ok {
			changes[i].Additions = stat.Additions
			changes[i].Deletions = stat.Deletions
			changes[i].Binary = stat.Binary
		}
	}
	return changes, nil
}

func parseNameStatus(out []byte) ([]GitFileChange, error) {
	fields := splitNUL(out)
	changes := make([]GitFileChange, 0, len(fields)/2)
	for i := 0; i < len(fields); {
		rawStatus := fields[i]
		i++
		if rawStatus == "" || i >= len(fields) {
			return nil, fmt.Errorf("unexpected name-status output")
		}
		code := rawStatus[0]
		change := GitFileChange{Status: statusName(code)}
		if len(rawStatus) > 1 {
			change.Similarity, _ = strconv.Atoi(rawStatus[1:])
		}
		if code == 'R' || code == 'C' {
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("rename/copy missing paths")
			}
			change.OldPath, change.Path = fields[i], fields[i+1]
			i += 2
		} else {
			change.Path = fields[i]
			i++
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func statusName(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'M':
		return "modified"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	case 'U':
		return "unmerged"
	default:
		return "unknown"
	}
}

type fileStat struct {
	Additions int
	Deletions int
	Binary    bool
}

func parseNumstat(out []byte) (map[string]fileStat, error) {
	fields := splitNUL(out)
	stats := make(map[string]fileStat, len(fields))
	for i := 0; i < len(fields); {
		parts := strings.SplitN(fields[i], "\t", 3)
		i++
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected numstat output")
		}
		stat := fileStat{}
		if parts[0] == "-" && parts[1] == "-" {
			stat.Binary = true
		} else {
			var err error
			stat.Additions, err = strconv.Atoi(parts[0])
			if err != nil {
				return nil, fmt.Errorf("parse additions: %w", err)
			}
			stat.Deletions, err = strconv.Atoi(parts[1])
			if err != nil {
				return nil, fmt.Errorf("parse deletions: %w", err)
			}
		}
		oldPath, path := "", parts[2]
		if path == "" {
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("rename numstat missing paths")
			}
			oldPath, path = fields[i], fields[i+1]
			i += 2
		}
		stats[changeKey(oldPath, path)] = stat
	}
	return stats, nil
}

func splitNUL(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	parts := strings.Split(string(out), "\x00")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func changeKey(oldPath, path string) string {
	return oldPath + "\x00" + path
}
