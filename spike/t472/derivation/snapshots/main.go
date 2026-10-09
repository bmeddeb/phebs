// Command t472snapshots authors and validates the T47.2b per-repository
// committed inputs layout-snapshot.json (t20-layout-snapshot-v1) and
// generated-from-snapshot.json (t20-generated-from-v1) from the frozen
// resolution tables under the worktree-external rehearsal directory.
//
// The frozen tuple is file-granular: because the .proto declarations are
// co-located with their *_grpc.pb.go clients in etcd, grpc-go, containerd,
// and istio, directory-granular roots could not express an idl and a
// generated root without overlapping; production accepts a file path as a
// root because pathWithinRoot includes equality and every consumer matches
// roots against the exact immutable tree.
//
// Every gate below mirrors a production gate exactly: the strict decoders
// and limits are the shipped resolverinput functions, and the semantic
// checks re-implement the shipped refusal conditions from extract's
// loadRoots/loadGeneratedFrom and resolvermaterialize's validateLayout.
// The tool refuses to write anything when any gate fails.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

const (
	// Mirrors internal/extract maxAttributionRoots / materialize MaxLayoutRoots.
	limitLayoutRoots = 128
	// Mirrors internal/extract maxAttributionMappings / materialize MaxGeneratedMappings.
	limitGeneratedMappings = 25_000
	// Mirrors internal/extract maxAttributionRoots (invocation branch) /
	// materialize MaxGeneratorInvocations.
	limitGeneratorInvocations = 128
	// T47.2b section 2 corpus bounds.
	boundBlobBytes = 10 << 20
	boundFiles     = 200_000
)

type resolutionTable struct {
	Repo        string `json:"repo"`
	Clients     int    `json:"clients"`
	Mapped      int    `json:"mapped"`
	Abstained   int    `json:"abstained"`
	Roots       int    `json:"roots"`
	Abstentions []struct {
		GeneratedHeader bool   `json:"generated_header"`
		GeneratedPath   string `json:"generated_path"`
		Reason          string `json:"reason"`
		SourceLine      string `json:"source_line"`
		Vendored        bool   `json:"vendored"`
	} `json:"abstentions"`
	Mappings []struct {
		DeclarationPath       string `json:"declaration_path"`
		GeneratedPath         string `json:"generated_path"`
		GeneratorRelativePath string `json:"generator_relative_path"`
		Vendored              bool   `json:"vendored"`
	} `json:"mappings"`
}

type treeFile struct {
	mode  string
	bytes int64
}

type stats struct {
	Repo               string   `json:"repo"`
	Commit             string   `json:"commit"`
	Clone              string   `json:"clone"`
	Clients            int      `json:"clients"`
	Mapped             int      `json:"mapped"`
	Abstained          int      `json:"abstained"`
	Mappings           int      `json:"mappings"`
	Roots              int      `json:"roots"`
	RootsDeduped       int      `json:"roots_deduped"`
	RegularFiles       int      `json:"regular_files"`
	Gitlinks           int      `json:"gitlinks"`
	Symlinks           int      `json:"symlinks"`
	MaxBlobPath        string   `json:"max_blob_path"`
	MaxBlobBytes       int64    `json:"max_blob_bytes"`
	OverBlobBound      []string `json:"over_blob_bound"`
	FilesWithinBound   bool     `json:"files_within_bound"`
	BlobsWithinBound   bool     `json:"blobs_within_bound"`
	LayoutSHA256       string   `json:"layout_sha256"`
	LayoutBytes        int      `json:"layout_bytes"`
	GeneratedSHA256    string   `json:"generated_from_sha256"`
	GeneratedBytes     int      `json:"generated_from_bytes"`
	Gates              []string `json:"gates"`
	AbstentionSummary  []string `json:"abstention_summary"`
	VendoredMapped     int      `json:"vendored_mapped"`
	NonVendoredMapped  int      `json:"nonvendored_mapped"`
	CgoOutOfTreeUnseen string   `json:"cgo_out_of_tree"`
}

func main() {
	repo := flag.String("repo", "", "repository short name")
	mappingsDir := flag.String("mappings", "", "directory holding <repo>.json")
	outDir := flag.String("out", "", "output directory for <repo>/{layout,generated-from}-snapshot.json")
	clone := flag.String("clone", "", "corpus clone to list")
	commit := flag.String("commit", "", "pinned commit")
	flag.Parse()
	if *repo == "" || *mappingsDir == "" || *outDir == "" || *clone == "" || *commit == "" {
		fatal("all of --repo --mappings --out --clone --commit are required")
	}

	table := readTable(path.Join(*mappingsDir, *repo+".json"))
	if table.Repo != *repo {
		fatal("table repo %q does not match %q", table.Repo, *repo)
	}
	if table.Clients != table.Mapped+table.Abstained ||
		table.Mapped != len(table.Mappings) {
		fatal("inconsistent table counts: clients=%d mapped=%d abstained=%d rows=%d",
			table.Clients, table.Mapped, table.Abstained, len(table.Mappings))
	}

	roots, deduped := buildRoots(table)
	mappings, vendors := buildMappings(table)
	if len(roots) > limitLayoutRoots {
		fatal("root count %d exceeds %d", len(roots), limitLayoutRoots)
	}
	if len(mappings) > limitGeneratedMappings {
		fatal("mapping count %d exceeds %d", len(mappings), limitGeneratedMappings)
	}

	layout := resolverinput.LayoutSnapshot{
		Version: resolverinput.LayoutSnapshotVersion,
		Roots:   roots,
	}
	generated := resolverinput.GeneratedFromSnapshot{
		Version:  resolverinput.GeneratedFromSnapshotVersion,
		Mappings: mappings,
	}
	layoutBytes := marshal(layout)
	generatedBytes := marshal(generated)

	files, gitlinks, symlinks, err := listTree(*clone, *commit)
	if err != nil {
		fatal("list tree: %v", err)
	}

	limits := resolverinput.SnapshotLimits{
		LayoutRoots:          limitLayoutRoots,
		GeneratedMappings:    limitGeneratedMappings,
		GeneratorInvocations: limitGeneratorInvocations,
	}
	var gates []string
	decodedLayout, err := resolverinput.DecodeLayoutSnapshot(string(layoutBytes), limits)
	if err != nil {
		fatal("round-trip layout decode: %v", err)
	}
	gates = append(gates, "decode_layout_production_decoder")
	if decodedLayout.Version != resolverinput.LayoutSnapshotVersion {
		fatal("layout version mismatch after decode")
	}
	decodedGenerated, err := resolverinput.DecodeGeneratedFromSnapshot(string(generatedBytes), limits)
	if err != nil {
		fatal("round-trip generated-from decode: %v", err)
	}
	gates = append(gates, "decode_generated_from_production_decoder")
	if decodedGenerated.Version != resolverinput.GeneratedFromSnapshotVersion {
		fatal("generated-from version mismatch after decode")
	}
	gates = append(gates,
		"layout_root_limit", "generated_mapping_limit", "invocation_limit_empty",
		"known_kinds_and_protocols", "source_protocol_empty", "roots_match_regular_files",
		"roots_pairwise_non_overlapping", "mapping_paths_valid_and_regular",
		"grpc_generator_relative_path_suffix", "protocol_derives_grpc",
		"layout_coverage_generated_idl_grpc",
	)
	if err := checkRoots(decodedLayout.Roots, files); err != nil {
		fatal("layout gate: %v", err)
	}
	if err := checkMappings(decodedGenerated, files, decodedLayout.Roots); err != nil {
		fatal("generated-from gate: %v", err)
	}

	overBound, maxPath, maxBytes := inventory(files)
	st := stats{
		Repo: *repo, Commit: *commit, Clone: *clone,
		Clients: table.Clients, Mapped: table.Mapped, Abstained: table.Abstained,
		Mappings: len(mappings), Roots: len(roots), RootsDeduped: deduped,
		RegularFiles: len(files), Gitlinks: gitlinks, Symlinks: symlinks,
		MaxBlobPath: maxPath, MaxBlobBytes: maxBytes, OverBlobBound: overBound,
		FilesWithinBound: len(files) <= boundFiles,
		BlobsWithinBound: len(overBound) == 0,
		LayoutSHA256:     digest(layoutBytes), LayoutBytes: len(layoutBytes),
		GeneratedSHA256: digest(generatedBytes), GeneratedBytes: len(generatedBytes),
		Gates:          gates,
		VendoredMapped: vendors[0], NonVendoredMapped: vendors[1],
		CgoOutOfTreeUnseen: "cgo build-cache documents are a scipmerge concern, not an input concern",
	}
	for _, abstention := range table.Abstentions {
		sourceLine := abstention.SourceLine
		if sourceLine == "" {
			sourceLine = "(no source line)"
		}
		st.AbstentionSummary = append(st.AbstentionSummary, fmt.Sprintf(
			"%s (%s, %s, vendored=%t, header=%t)",
			abstention.GeneratedPath, abstention.Reason, sourceLine,
			abstention.Vendored, abstention.GeneratedHeader,
		))
	}

	repoDir := path.Join(*outDir, *repo)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		fatal("mkdir: %v", err)
	}
	writeFile(path.Join(repoDir, "layout-snapshot.json"), layoutBytes)
	writeFile(path.Join(repoDir, "generated-from-snapshot.json"), generatedBytes)
	statsBytes := marshal(st)
	writeFile(path.Join(*outDir, *repo+".stats.json"), statsBytes)
	os.Stdout.Write(statsBytes)
}

// buildRoots mirrors the frozen decision: one generated root and one idl
// root per mapped pair, file-granular, protocol grpc, deduped and sorted by
// (path, kind, protocol) exactly as validateLayout canonicalizes.
func buildRoots(table resolutionTable) ([]resolverinput.LayoutRoot, int) {
	seen := make(map[resolverinput.LayoutRoot]struct{})
	for _, row := range table.Mappings {
		for _, root := range []resolverinput.LayoutRoot{
			{Kind: "generated", Path: row.GeneratedPath, Protocol: "grpc"},
			{Kind: "idl", Path: row.DeclarationPath, Protocol: "grpc"},
		} {
			seen[root] = struct{}{}
		}
	}
	roots := make([]resolverinput.LayoutRoot, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Path != roots[j].Path {
			return roots[i].Path < roots[j].Path
		}
		if roots[i].Kind != roots[j].Kind {
			return roots[i].Kind < roots[j].Kind
		}
		return roots[i].Protocol < roots[j].Protocol
	})
	return roots, len(table.Mappings)*2 - len(roots)
}

func buildMappings(table resolutionTable) ([]resolverinput.GeneratedFromMapping, [2]int) {
	seen := make(map[resolverinput.GeneratedFromMapping]struct{}, len(table.Mappings))
	mappings := make([]resolverinput.GeneratedFromMapping, 0, len(table.Mappings))
	var vendors [2]int
	for index, row := range table.Mappings {
		mapping := resolverinput.GeneratedFromMapping{
			GeneratedPath:         row.GeneratedPath,
			GeneratorRelativePath: row.GeneratorRelativePath,
			DeclarationPath:       row.DeclarationPath,
		}
		if _, duplicate := seen[mapping]; duplicate {
			fatal("mapping %d duplicates an earlier relation %q", index, row.GeneratedPath)
		}
		seen[mapping] = struct{}{}
		if row.Vendored {
			vendors[0]++
		} else {
			vendors[1]++
		}
		mappings = append(mappings, mapping)
	}
	sort.Slice(mappings, func(i, j int) bool {
		if mappings[i].GeneratedPath != mappings[j].GeneratedPath {
			return mappings[i].GeneratedPath < mappings[j].GeneratedPath
		}
		if mappings[i].DeclarationPath != mappings[j].DeclarationPath {
			return mappings[i].DeclarationPath < mappings[j].DeclarationPath
		}
		return mappings[i].GeneratorRelativePath < mappings[j].GeneratorRelativePath
	})
	return mappings, vendors
}

// checkRoots mirrors extract loadRoots and materialize validateLayout.
func checkRoots(roots []resolverinput.LayoutRoot, files map[string]treeFile) error {
	if len(roots) > limitLayoutRoots {
		return fmt.Errorf("more than %d layout roots", limitLayoutRoots)
	}
	for index, root := range roots {
		if repopath.Validate(root.Path) != nil {
			return fmt.Errorf("root %d path %q is not a valid corpus path", index, root.Path)
		}
		switch root.Kind {
		case "source":
			if root.Protocol != "" {
				return fmt.Errorf("source root %q must not name a protocol", root.Path)
			}
		case "idl", "generated":
			if !validToken(root.Protocol) {
				return fmt.Errorf("%s root %q requires a valid protocol", root.Kind, root.Path)
			}
		default:
			return fmt.Errorf("root %d has unsupported kind %q", index, root.Kind)
		}
		matched := false
		for filePath := range files {
			if pathWithinRoot(filePath, root.Path) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("root %q matches no regular corpus file", root.Path)
		}
	}
	sorted := append([]resolverinput.LayoutRoot(nil), roots...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Protocol < sorted[j].Protocol
	})
	for left := range sorted {
		for right := left + 1; right < len(sorted); right++ {
			if rootsOverlap(sorted[left].Path, sorted[right].Path) {
				return fmt.Errorf("layout roots %q and %q overlap",
					sorted[left].Path, sorted[right].Path)
			}
		}
	}
	return nil
}

// checkMappings mirrors extract loadGeneratedFrom for a snapshot built from
// direct mappings only (no invocations).
func checkMappings(
	generated resolverinput.GeneratedFromSnapshot,
	files map[string]treeFile,
	roots []resolverinput.LayoutRoot,
) error {
	if len(generated.Invocations) > 0 {
		return errors.New("invocations are not part of the frozen tuple")
	}
	seen := make(map[string]struct{}, len(generated.Mappings))
	for index, mapping := range generated.Mappings {
		for label, value := range map[string]string{
			"generated path": mapping.GeneratedPath, "declaration path": mapping.DeclarationPath,
		} {
			if repopath.Validate(value) != nil {
				return fmt.Errorf("mapping %d %s %q is not a valid corpus path", index, label, value)
			}
			if _, present := files[value]; !present {
				return fmt.Errorf("mapping %d %s %q is absent from the corpus", index, label, value)
			}
		}
		protocol, err := derivedProtocol(mapping.Protocol, mapping.DeclarationPath)
		if err != nil {
			return fmt.Errorf("mapping %d: %w", index, err)
		}
		if len(roots) > 0 {
			generatedRoot, generatedOK := classify(roots, mapping.GeneratedPath)
			declarationRoot, declaredOK := classify(roots, mapping.DeclarationPath)
			if !generatedOK || generatedRoot.Kind != "generated" ||
				generatedRoot.Protocol != protocol {
				return fmt.Errorf("mapping %d generated path %q has no matching generated/%s root",
					index, mapping.GeneratedPath, protocol)
			}
			if !declaredOK || declarationRoot.Kind != "idl" ||
				declarationRoot.Protocol != protocol {
				return fmt.Errorf("mapping %d declaration path %q has no matching idl/%s root",
					index, mapping.DeclarationPath, protocol)
			}
		}
		if mapping.GeneratorRelativePath != "" {
			if repopath.Validate(mapping.GeneratorRelativePath) != nil {
				return fmt.Errorf("mapping %d generator-relative path %q is not a valid corpus path",
					index, mapping.GeneratorRelativePath)
			}
		}
		if protocol == "grpc" && mapping.GeneratorRelativePath != "" &&
			!strings.HasSuffix(mapping.GeneratorRelativePath, ".proto") {
			return fmt.Errorf("mapping %d protobuf generator-relative path is not .proto", index)
		}
		if protocol == "thrift" && mapping.GeneratorRelativePath != "" {
			return fmt.Errorf("mapping %d Thrift relation cannot use a generator-relative path", index)
		}
		id := protocol + "\x00" + mapping.GeneratedPath + "\x00" +
			mapping.GeneratorRelativePath + "\x00" + mapping.DeclarationPath
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("mapping %d duplicates an earlier generated-from relation", index)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func classify(
	roots []resolverinput.LayoutRoot,
	filePath string,
) (resolverinput.LayoutRoot, bool) {
	for _, root := range roots {
		if pathWithinRoot(filePath, root.Path) {
			return root, true
		}
	}
	return resolverinput.LayoutRoot{}, false
}

func pathWithinRoot(filePath, root string) bool {
	return filePath == root || strings.HasPrefix(filePath, root+"/")
}

func rootsOverlap(left, right string) bool {
	return pathWithinRoot(left, right) || pathWithinRoot(right, left)
}

func derivedProtocol(declared, declarationPath string) (string, error) {
	derived := ""
	switch {
	case strings.HasSuffix(declarationPath, ".proto"):
		derived = "grpc"
	case strings.HasSuffix(declarationPath, ".thrift"):
		derived = "thrift"
	default:
		return "", fmt.Errorf("declaration path %q has no supported protocol suffix", declarationPath)
	}
	if declared != "" && declared != derived {
		return "", fmt.Errorf("declared protocol %q conflicts with %q", declared, derived)
	}
	return derived, nil
}

// validToken mirrors extract's protocol-token grammar.
func validToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// listTree runs the same immutable-tree listing the production corpus uses
// and keeps only regular files (100644/100755) in the corpus-file set.
func listTree(clone, commit string) (map[string]treeFile, int, int, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "-l", "-z", "--full-tree", commit)
	cmd.Dir = clone
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("git ls-tree: %w", err)
	}
	files := make(map[string]treeFile)
	gitlinks, symlinks := 0, 0
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		meta, filePath, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, 0, 0, fmt.Errorf("unrecognized tree record %q", record)
		}
		fields := strings.Fields(meta)
		if len(fields) != 4 {
			return nil, 0, 0, fmt.Errorf("unrecognized tree record metadata %q", meta)
		}
		switch fields[0] {
		case "100644", "100755":
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("tree record size %q: %w", fields[3], err)
			}
			files[filePath] = treeFile{mode: fields[0], bytes: size}
		case "120000":
			symlinks++
		case "160000":
			gitlinks++
		default:
			return nil, 0, 0, fmt.Errorf("unexpected tree mode %q for %q", fields[0], filePath)
		}
	}
	return files, gitlinks, symlinks, nil
}

func inventory(files map[string]treeFile) ([]string, string, int64) {
	var over []string
	maxPath := ""
	var maxBytes int64
	for filePath, record := range files {
		if record.bytes > boundBlobBytes {
			over = append(over, fmt.Sprintf("%s (%d bytes)", filePath, record.bytes))
		}
		if record.bytes > maxBytes || record.bytes == maxBytes && filePath > maxPath {
			maxPath, maxBytes = filePath, record.bytes
		}
	}
	sort.Strings(over)
	return over, maxPath, maxBytes
}

func readTable(filePath string) resolutionTable {
	content, err := os.ReadFile(filePath)
	if err != nil {
		fatal("read table: %v", err)
	}
	var table resolutionTable
	if err := json.Unmarshal(content, &table); err != nil {
		fatal("parse table: %v", err)
	}
	return table
}

func marshal(value any) []byte {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal("marshal: %v", err)
	}
	return append(content, '\n')
}

func writeFile(filePath string, content []byte) {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fatal("write %s: %v", filePath, err)
	}
	if _, err := file.Write(content); err != nil {
		fatal("write %s: %v", filePath, err)
	}
	if err := file.Sync(); err != nil {
		fatal("sync %s: %v", filePath, err)
	}
	if err := file.Close(); err != nil {
		fatal("close %s: %v", filePath, err)
	}
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "t472snapshots: "+format+"\n", args...)
	os.Exit(1)
}
