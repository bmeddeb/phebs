// Package compat runs protobuf wire-compatibility checks through the pinned
// Buf CLI. Repository content is never executed: callers supply bounded
// .proto text, which is copied into a fresh sanitized tree before the one
// fixed `buf breaking` command is launched by the OS sandbox.
package compat

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

const (
	// Version is intentionally coupled to the go.mod tool pin. A different
	// binary is rejected before it can produce a verdict.
	Version = "1.72.0"

	Policy = "WIRE"

	maxFilesPerSnapshot = 256
	maxFileBytes        = idlpreflight.MaxFileBytes
	maxInputBytes       = 32 << 20
	maxViolations       = 5_000
	maxAffectedFields   = 256
	maxJSONLineBytes    = 1 << 20
	checkTimeout        = 15 * time.Second

	fixedConfig = `{"version":"v2","breaking":{"use":["WIRE"]}}`
)

var (
	ErrInvalidInput = errors.New("invalid compatibility input")
	ErrUnavailable  = errors.New("compatibility checker unavailable")
	ErrLimit        = errors.New("compatibility checker limit exceeded")
	ErrCheckFailed  = errors.New("compatibility checker failed")
)

// File is one caller-supplied protobuf source file. Path is a canonical
// repository-relative slash path; Content is never retained in the result.
type File struct {
	Path    string `json:"path" minLength:"1" maxLength:"4096" jsonschema:"canonical slash-separated relative .proto path"`
	Content string `json:"content" maxLength:"4194304" jsonschema:"UTF-8 protobuf source text; maximum 4 MiB"`
}

// Request compares After against Before. Lineage binds affected field numbers
// to the same version-independent identity used by SCIP consumer evidence.
type Request struct {
	Lineage string `json:"lineage" minLength:"1" maxLength:"1024" jsonschema:"canonical contract lineage id shared by the compared descriptor sets"`
	Before  []File `json:"before" maxItems:"256" jsonschema:"baseline protobuf source files"`
	After   []File `json:"after" maxItems:"256" jsonschema:"candidate protobuf source files"`
}

// InputFile records only the canonical path and content digest of an input.
type InputFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// InputSnapshot is the content commitment for one side of the comparison.
type InputSnapshot struct {
	Digest string      `json:"digest"`
	Files  []InputFile `json:"files"`
}

// FieldIdentity is the stable consumer-join key. A source field name and type
// can change between versions; message full name and wire number do not.
type FieldIdentity struct {
	Lineage string `json:"lineage"`
	Message string `json:"message"`
	Number  int    `json:"field_number"`
}

// Violation is one structured Buf breaking finding. Spans are one-based and
// refer to a file in the named input snapshot.
type Violation struct {
	Snapshot    string         `json:"snapshot"`
	Path        string         `json:"path"`
	StartLine   int            `json:"start_line"`
	StartColumn int            `json:"start_column"`
	EndLine     int            `json:"end_line"`
	EndColumn   int            `json:"end_column"`
	Rule        string         `json:"rule"`
	Message     string         `json:"message"`
	Field       *FieldIdentity `json:"affected_field,omitempty"`
}

// Run is deterministic invocation provenance embedded in the immutable proof
// bundle. Arguments are the exact relative arguments supplied to Buf, so they
// disclose no host temp path and remain byte-stable for identical inputs.
type Run struct {
	Engine    string   `json:"engine"`
	Version   string   `json:"version"`
	Policy    string   `json:"policy"`
	Arguments []string `json:"arguments"`
	ExitCode  int      `json:"exit_code"`
	Result    string   `json:"result"`
}

// CompatibilityResult is the specification verdict plus stable consumer join keys.
type CompatibilityResult struct {
	Compatible     bool            `json:"compatible"`
	Before         InputSnapshot   `json:"before"`
	After          InputSnapshot   `json:"after"`
	Violations     []Violation     `json:"violations"`
	AffectedFields []FieldIdentity `json:"affected_fields"`
	Run            Run             `json:"extraction_run"`
}

// Limits is the public, deterministic compatibility/preflight contract shown
// beside Workbench proposal previews.
type Limits struct {
	Engine                string `json:"engine"`
	Version               string `json:"version"`
	Policy                string `json:"policy"`
	MaxFilesPerSnapshot   int    `json:"max_files_per_snapshot"`
	MaxFileBytes          int    `json:"max_file_bytes"`
	MaxInputBytes         int    `json:"max_input_bytes"`
	MaxTokensPerFile      int    `json:"max_tokens_per_file"`
	MaxStructuralDepth    int    `json:"max_structural_depth"`
	MaxViolations         int    `json:"max_violations"`
	MaxAffectedFieldCount int    `json:"max_affected_field_count"`
}

// WireLimits returns the frozen Buf WIRE and parser ceilings without probing
// or executing the child binary.
func WireLimits() Limits {
	return Limits{
		Engine: "buf", Version: Version, Policy: Policy,
		MaxFilesPerSnapshot:   maxFilesPerSnapshot,
		MaxFileBytes:          maxFileBytes,
		MaxInputBytes:         maxInputBytes,
		MaxTokensPerFile:      idlpreflight.MaxTokens,
		MaxStructuralDepth:    idlpreflight.MaxStructuralDepth,
		MaxViolations:         maxViolations,
		MaxAffectedFieldCount: maxAffectedFields,
	}
}

// WirePolicyDigest binds the exact pinned engine, policy configuration, and
// visible limits used by both proof-bundle and Workbench callers.
func WirePolicyDigest() string {
	encoded, _ := json.Marshal(struct {
		Config string `json:"config"`
		Limits Limits `json:"limits"`
	}{fixedConfig, WireLimits()})
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PreparedRequest is a pure, source-free commitment produced before Buf is
// invoked. The canonical request and parser metadata remain private so callers
// cannot bypass Check's execution boundary.
type PreparedRequest struct {
	Before InputSnapshot `json:"before"`
	After  InputSnapshot `json:"after"`

	request      Request
	declarations []fieldDeclaration
}

// Prepare applies the exact canonicalization, aggregate bounds, lexical
// preflight, and in-process parser work used by Check without executing Buf.
func Prepare(ctx context.Context, request Request) (*PreparedRequest, error) {
	if !validLineage(request.Lineage) {
		return nil, fmt.Errorf(
			"%w: lineage must be a bounded canonical identity",
			ErrInvalidInput,
		)
	}
	before, err := canonicalFiles(request.Before)
	if err != nil {
		return nil, fmt.Errorf("%w: before: %v", ErrInvalidInput, err)
	}
	after, err := canonicalFiles(request.After)
	if err != nil {
		return nil, fmt.Errorf("%w: after: %v", ErrInvalidInput, err)
	}
	if len(before)+len(after) == 0 {
		return nil, fmt.Errorf(
			"%w: at least one .proto file is required",
			ErrInvalidInput,
		)
	}
	total := totalBytes(before) + totalBytes(after)
	if total > maxInputBytes {
		return nil, fmt.Errorf(
			"%w: inputs exceed %d bytes",
			ErrLimit,
			maxInputBytes,
		)
	}
	declarations, err := fieldDeclarations(ctx, before, after)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: parse field identities: %v",
			ErrInvalidInput,
			err,
		)
	}
	return &PreparedRequest{
		Before: snapshotCommitment(before),
		After:  snapshotCommitment(after),
		request: Request{
			Lineage: request.Lineage,
			Before:  before,
			After:   after,
		},
		declarations: declarations,
	}, nil
}

// Service is the narrow boundary used by the shared HTTP/MCP proof service.
type Service interface {
	Check(ctx context.Context, request Request) (*CompatibilityResult, error)
}

// Checker invokes one pinned Buf binary through the platform sandbox.
type Checker struct {
	bin     string
	tempDir string
}

// FindBinary locates Buf using the same deployment pattern as the zoekt child:
// an explicit override, beside phebs, under its bin directory, then PATH.
func FindBinary() (string, error) {
	if configured := os.Getenv("PHEBS_BUF"); configured != "" {
		return executablePath(configured)
	}
	if current, err := os.Executable(); err == nil {
		dir := filepath.Dir(current)
		for _, candidate := range []string{filepath.Join(dir, "buf"), filepath.Join(dir, "bin", "buf")} {
			if resolved, err := executablePath(candidate); err == nil {
				return resolved, nil
			}
		}
	}
	resolved, err := exec.LookPath("buf")
	if err != nil {
		return "", fmt.Errorf("%w: find buf: %v", ErrUnavailable, err)
	}
	return executablePath(resolved)
}

func executablePath(candidate string) (string, error) {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: resolve buf path: %v", ErrUnavailable, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: resolve buf binary: %v", ErrUnavailable, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%w: buf path is not an executable regular file", ErrUnavailable)
	}
	return resolved, nil
}

// New constructs a checker only when the host can provide the required
// network/write sandbox and process resource boundary.
func New(bin string) (*Checker, error) {
	resolved, err := executablePath(bin)
	if err != nil {
		return nil, err
	}
	if err := sandboxSupported(); err != nil {
		return nil, err
	}
	return &Checker{bin: resolved}, nil
}

// Validate performs the same sandboxed version probe used by Check. Servers
// call it before registering HTTP/MCP capability, so discovery never exposes
// a tool backed by a missing, mismatched, or non-enforceable child.
func (c *Checker) Validate(ctx context.Context) error {
	if c == nil || c.bin == "" {
		return fmt.Errorf("%w: no configured checker", ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	root, cleanup, err := c.sanitizedRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	version, err := c.version(ctx, root)
	if err != nil {
		return err
	}
	if version != Version {
		return fmt.Errorf("%w: buf version %q does not match pinned %q", ErrUnavailable, version, Version)
	}
	return nil
}

// Check validates and commits both input sets before launching Buf. Every
// error is classified so transports can distinguish bad input, bounded
// refusal, unavailable host isolation, and an engine failure.
func (c *Checker) Check(ctx context.Context, request Request) (*CompatibilityResult, error) {
	if c == nil || c.bin == "" {
		return nil, fmt.Errorf("%w: no configured checker", ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	prepared, err := Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	before, after := prepared.request.Before, prepared.request.After

	root, cleanup, err := c.sanitizedRoot()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := writeFiles(filepath.Join(root, "before"), before); err != nil {
		return nil, fmt.Errorf("%w: stage before input: %v", ErrInvalidInput, err)
	}
	if err := writeFiles(filepath.Join(root, "after"), after); err != nil {
		return nil, fmt.Errorf("%w: stage after input: %v", ErrInvalidInput, err)
	}

	version, err := c.version(ctx, root)
	if err != nil {
		return nil, err
	}
	if version != Version {
		return nil, fmt.Errorf("%w: buf version %q does not match pinned %q", ErrUnavailable, version, Version)
	}

	arguments := []string{
		"breaking", "../after", "--against", "../before",
		"--config", fixedConfig, "--against-config", fixedConfig,
		"--disable-symlinks", "--error-format=json", "--timeout=10s",
	}
	output, err := runSandboxed(ctx, root, c.bin, arguments)
	if err != nil {
		return nil, err
	}
	if output.overflow {
		return nil, fmt.Errorf("%w: buf output exceeds its bounded limit", ErrLimit)
	}
	if output.exitCode != 0 && output.exitCode != 100 {
		detail := sanitizedDetail(root, output.stderr)
		if detail == "" {
			detail = "buf returned a non-verdict exit status"
		}
		return nil, fmt.Errorf("%w: %s (exit %d)", ErrCheckFailed, detail, output.exitCode)
	}
	violations, err := parseViolations(
		output.stdout,
		prepared.declarations,
		prepared.request.Lineage,
		before,
		after,
	)
	if err != nil {
		return nil, err
	}
	if output.exitCode == 0 && len(violations) != 0 || output.exitCode == 100 && len(violations) == 0 {
		return nil, fmt.Errorf("%w: Buf exit status and structured findings disagree", ErrCheckFailed)
	}
	resultName := "compatible"
	if output.exitCode == 100 {
		resultName = "breaking"
	}
	fields := affectedFields(violations)
	if len(fields) > maxAffectedFields {
		return nil, fmt.Errorf("%w: more than %d affected fields", ErrLimit, maxAffectedFields)
	}
	return &CompatibilityResult{
		Compatible: output.exitCode == 0,
		Before:     prepared.Before, After: prepared.After,
		Violations: violations, AffectedFields: fields,
		Run: Run{
			Engine: "buf", Version: version, Policy: Policy,
			Arguments: append([]string(nil), arguments...), ExitCode: output.exitCode, Result: resultName,
		},
	}, nil
}

func (c *Checker) sanitizedRoot() (string, func(), error) {
	original, err := os.MkdirTemp(c.tempDir, "phebs-buf-")
	if err != nil {
		return "", nil, fmt.Errorf("%w: create sanitized tree: %v", ErrCheckFailed, err)
	}
	cleanup := func() { _ = os.RemoveAll(original) }
	// macOS commonly exposes /var as a symlink to /private/var. Seatbelt
	// compares canonical paths, so use the resolved spelling for its subpath
	// rule and every child argument while retaining the original for cleanup.
	root, err := filepath.EvalSymlinks(original)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%w: resolve sanitized tree: %v", ErrCheckFailed, err)
	}
	for _, name := range []string{"before", "after", "exec", "home", "cache", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("%w: create sanitized tree: %v", ErrCheckFailed, err)
		}
	}
	return root, cleanup, nil
}

func validLineage(value string) bool {
	if value == "" || len(value) > 1_024 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == '/' || r == '#' {
			return false
		}
	}
	return true
}

func (c *Checker) version(ctx context.Context, root string) (string, error) {
	output, err := runSandboxed(ctx, root, c.bin, []string{"--version"})
	if err != nil {
		return "", err
	}
	if output.overflow || output.exitCode != 0 {
		return "", fmt.Errorf("%w: execute buf --version", ErrUnavailable)
	}
	version := strings.TrimSpace(output.stdout)
	if strings.ContainsAny(version, "\r\n\t ") || version == "" || len(version) > 64 {
		return "", fmt.Errorf("%w: invalid buf version output", ErrUnavailable)
	}
	return version, nil
}

func canonicalFiles(input []File) ([]File, error) {
	if len(input) > maxFilesPerSnapshot {
		return nil, fmt.Errorf("more than %d files", maxFilesPerSnapshot)
	}
	files := append([]File(nil), input...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := range files {
		if err := validatePath(files[i].Path); err != nil {
			return nil, err
		}
		if i > 0 && files[i-1].Path == files[i].Path {
			return nil, fmt.Errorf("duplicate path %q", files[i].Path)
		}
		if len(files[i].Content) > maxFileBytes {
			return nil, fmt.Errorf("%s exceeds %d bytes", files[i].Path, maxFileBytes)
		}
		if !utf8.ValidString(files[i].Content) {
			return nil, fmt.Errorf("%s is not UTF-8 text", files[i].Path)
		}
	}
	return files, nil
}

func validatePath(value string) error {
	if value == "" || len(value) > 4_096 || !utf8.ValidString(value) ||
		path.Clean(value) != value || path.IsAbs(value) || strings.Contains(value, "\\") ||
		!strings.HasSuffix(value, ".proto") {
		return fmt.Errorf("path %q is not a canonical relative .proto path", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path %q contains an unsafe segment", value)
		}
	}
	for _, r := range value {
		if r < ' ' || r == 0x7f {
			return fmt.Errorf("path %q contains control characters", value)
		}
	}
	return nil
}

func totalBytes(files []File) int {
	total := 0
	for _, file := range files {
		total += len(file.Content)
	}
	return total
}

func writeFiles(root string, files []File) error {
	for _, file := range files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		handle, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(handle, file.Content)
		closeErr := handle.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func snapshotCommitment(files []File) InputSnapshot {
	result := InputSnapshot{Files: make([]InputFile, 0, len(files))}
	h := sha256.New()
	for _, file := range files {
		digest := sha256.Sum256([]byte(file.Content))
		encoded := "sha256:" + hex.EncodeToString(digest[:])
		result.Files = append(result.Files, InputFile{Path: file.Path, Digest: encoded})
		_, _ = io.WriteString(h, strconv.Itoa(len(file.Path)))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, file.Path)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, encoded)
		_, _ = io.WriteString(h, "\x00")
	}
	result.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return result
}

type rawViolation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
	Type        string `json:"type"`
	Message     string `json:"message"`
}

func parseViolations(raw string, declarations []fieldDeclaration, lineage string, before, after []File) ([]Violation, error) {
	if raw == "" {
		return []Violation{}, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maxJSONLineBytes)
	paths := map[string]map[string]struct{}{"before": {}, "after": {}}
	for _, file := range before {
		paths["before"][file.Path] = struct{}{}
	}
	for _, file := range after {
		paths["after"][file.Path] = struct{}{}
	}
	violations := make([]Violation, 0)
	for scanner.Scan() {
		if len(violations) >= maxViolations {
			return nil, fmt.Errorf("%w: more than %d Buf findings", ErrLimit, maxViolations)
		}
		var finding rawViolation
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&finding); err != nil {
			return nil, fmt.Errorf("%w: decode Buf finding: %v", ErrCheckFailed, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: Buf finding has trailing JSON content", ErrCheckFailed)
		}
		snapshot, filePath, ok := findingPath(finding.Path)
		_, knownPath := paths[snapshot][filePath]
		if !ok || finding.Type == "" || finding.Message == "" ||
			!knownPath ||
			finding.StartLine < 1 || finding.StartColumn < 1 ||
			finding.EndLine < finding.StartLine || finding.EndColumn < 1 ||
			finding.EndLine == finding.StartLine && finding.EndColumn < finding.StartColumn {
			return nil, fmt.Errorf("%w: Buf returned an invalid structured finding", ErrCheckFailed)
		}
		violation := Violation{
			Snapshot: snapshot, Path: filePath,
			StartLine: finding.StartLine, StartColumn: finding.StartColumn,
			EndLine: finding.EndLine, EndColumn: finding.EndColumn,
			Rule: finding.Type, Message: finding.Message,
		}
		if field, ok := matchField(declarations, snapshot, filePath, finding.StartLine, finding.StartColumn); ok {
			field.Lineage = lineage
			violation.Field = &field
		}
		violations = append(violations, violation)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan Buf findings: %v", ErrLimit, err)
	}
	sort.Slice(violations, func(i, j int) bool {
		left, right := violations[i], violations[j]
		if left.Snapshot != right.Snapshot {
			return left.Snapshot < right.Snapshot
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.StartLine != right.StartLine {
			return left.StartLine < right.StartLine
		}
		if left.StartColumn != right.StartColumn {
			return left.StartColumn < right.StartColumn
		}
		if left.EndLine != right.EndLine {
			return left.EndLine < right.EndLine
		}
		if left.EndColumn != right.EndColumn {
			return left.EndColumn < right.EndColumn
		}
		if left.Rule != right.Rule {
			return left.Rule < right.Rule
		}
		return left.Message < right.Message
	})
	return violations, nil
}

func findingPath(value string) (string, string, bool) {
	for _, prefix := range []string{"../after/", "../before/"} {
		if strings.HasPrefix(value, prefix) {
			filePath := strings.TrimPrefix(value, prefix)
			if validatePath(filePath) == nil {
				return strings.TrimSuffix(strings.TrimPrefix(prefix, "../"), "/"), filePath, true
			}
		}
	}
	return "", "", false
}

func affectedFields(violations []Violation) []FieldIdentity {
	seen := make(map[FieldIdentity]struct{})
	for _, violation := range violations {
		if violation.Field != nil && violation.Field.Lineage != "" {
			seen[*violation.Field] = struct{}{}
		}
	}
	result := make([]FieldIdentity, 0, len(seen))
	for field := range seen {
		result = append(result, field)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Lineage != result[j].Lineage {
			return result[i].Lineage < result[j].Lineage
		}
		if result[i].Message != result[j].Message {
			return result[i].Message < result[j].Message
		}
		return result[i].Number < result[j].Number
	})
	return result
}

func sanitizedDetail(root, value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, root, "<sandbox>"))
	if len(value) > 2_048 {
		value = value[:2_048]
	}
	return value
}
