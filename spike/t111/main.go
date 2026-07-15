package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: t111 <sync|hydrate|extract|identity|fields|verify|stats> [flags]")
	}
	toolCleanup, err := configureHarnessTools()
	if err != nil {
		fatal("configure exact toolchain: %v", err)
	}
	defer toolCleanup()
	base := filepath.Join("spike", "t111")
	lock := filepath.Join(base, "corpus.lock.json")
	corpusDir := filepath.Join(base, "corpus")
	outDir := filepath.Join(base, "out")
	repoRoot, err := filepath.Abs(".")
	if err != nil {
		fatal("resolve repository root: %v", err)
	}
	if os.Args[1] != "sync" && os.Args[1] != "hydrate" {
		if err := verifyRunningBoundHarness(repoRoot, harnessModuleCacheRoot); err != nil {
			fatal("%v", err)
		}
	}

	switch os.Args[1] {
	case "sync":
		entries, err := loadCorpus(lock)
		if err != nil {
			fatal("%v", err)
		}
		if err := syncCorpus(entries, corpusDir); err != nil {
			fatal("%v", err)
		}
	case "hydrate":
		fs := flag.NewFlagSet("hydrate", flag.ExitOnError)
		system := fs.String("system", "", "one corpus system name (default: all)")
		proxy := fs.String("proxy", "https://proxy.golang.org", "one explicit https:// or file:// Go module proxy")
		sumdb := fs.String("sumdb", "sum.golang.org", "explicit Go checksum database or off")
		_ = fs.Parse(os.Args[2:])
		entries, err := loadCorpus(lock)
		if err != nil {
			fatal("%v", err)
		}
		selected := entries
		if *system != "" {
			selected = nil
			for _, entry := range entries {
				if entry.Name == *system {
					selected = append(selected, entry)
					break
				}
			}
			if len(selected) == 0 {
				fatal("unknown system %q", *system)
			}
		}
		if err := hydrateHarnessModule(repoRoot, harnessModuleCacheRoot, *proxy, *sumdb); err != nil {
			fatal("%v", err)
		}
		for _, entry := range selected {
			root := filepath.Join(corpusDir, entry.Name)
			downloaded, vendored, err := hydrateCorpusModules(entry, root, harnessModuleCacheRoot, *proxy, *sumdb)
			if err != nil {
				fatal("hydrate %s: %v", entry.Name, err)
			}
			fmt.Printf("corpus %-18s hydrated %d modules (%d vendored, network-free)\n", entry.Name, downloaded, vendored)
		}
		if err := buildBoundHarnesses(repoRoot, harnessModuleCacheRoot); err != nil {
			fatal("build bound harnesses: %v", err)
		}
		fmt.Printf("bound producer and typed oracle → %s\n", filepath.Join(harnessModuleCacheRoot, "bin"))
	case "extract":
		fs := flag.NewFlagSet("extract", flag.ExitOnError)
		system := fs.String("system", "", "corpus system name (required)")
		protoOnly := fs.Bool("proto-only", false, "declared plane only (no Go type-checking)")
		_ = fs.Parse(os.Args[2:])
		if *system == "" {
			fatal("-system required")
		}
		entry, corpusRoot, root, cleanup, err := extractionSnapshot(lock, corpusDir, *system)
		if err != nil {
			fatal("%v", err)
		}
		defer cleanup()

		facts, err := extractProtoDecls(entry.Name, entry.Commit, root)
		if err != nil {
			fatal("proto: %v", err)
		}
		if !*protoOnly {
			context, err := corpusBuildContext(*entry)
			if err != nil {
				fatal("Go analysis policy: %v", err)
			}
			goFacts, err := extractGoGRPCWithContext(entry.Name, entry.Commit, root, context)
			if err != nil {
				fatal("go: %v", err)
			}
			facts = append(facts, goFacts...)
		}
		name := entry.Name + ".facts.jsonl"
		if *protoOnly {
			// A declared-plane diagnostic is not a replacement for a complete
			// operation-extraction run.
			name = entry.Name + ".proto.facts.jsonl"
		}
		outPath := filepath.Join(outDir, name)
		if err := validateAndPublish(*entry, corpusRoot, root, outPath, facts); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%s: %d facts → %s\n", entry.Name, len(facts), outPath)
		printStats(facts)
	case "identity":
		fs := flag.NewFlagSet("identity", flag.ExitOnError)
		system := fs.String("system", "", "corpus system name (required)")
		_ = fs.Parse(os.Args[2:])
		if *system == "" {
			fatal("-system required")
		}
		entry, corpusRoot, root, cleanup, err := extractionSnapshot(lock, corpusDir, *system)
		if err != nil {
			fatal("%v", err)
		}
		defer cleanup()
		facts, err := extractIdentity(entry.Name, entry.Commit, root, nil)
		if err != nil {
			fatal("identity: %v", err)
		}
		outPath := filepath.Join(outDir, entry.Name+".identity.jsonl")
		if err := validateAndPublish(*entry, corpusRoot, root, outPath, facts); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%s: %d identity facts → %s\n", entry.Name, len(facts), outPath)
		printStats(facts)
	case "fields":
		fs := flag.NewFlagSet("fields", flag.ExitOnError)
		system := fs.String("system", "", "corpus system name (required)")
		_ = fs.Parse(os.Args[2:])
		if *system == "" {
			fatal("-system required")
		}
		entry, corpusRoot, root, cleanup, err := extractionSnapshot(lock, corpusDir, *system)
		if err != nil {
			fatal("%v", err)
		}
		defer cleanup()
		var decls []Fact
		prior, err := readFacts(filepath.Join(outDir, entry.Name+".facts.jsonl"))
		if err != nil {
			fatal("read supporting facts: %v", err)
		}
		if err := verifyPinnedFacts(*entry, corpusRoot, prior); err != nil {
			fatal("verify supporting facts: %v", err)
		}
		for _, f := range prior {
			if f.Predicate == "DECLARES_FIELD" {
				decls = append(decls, f)
			}
		}
		context, err := corpusBuildContext(*entry)
		if err != nil {
			fatal("Go analysis policy: %v", err)
		}
		facts, err := extractFieldRefsWithContext(entry.Name, entry.Commit, root, decls, context)
		if err != nil {
			fatal("fields: %v", err)
		}
		outPath := filepath.Join(outDir, entry.Name+".fields.jsonl")
		if err := validateAndPublish(*entry, corpusRoot, root, outPath, facts); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%s: %d field facts → %s\n", entry.Name, len(facts), outPath)
		printStats(facts)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		system := fs.String("system", "", "corpus system name (required)")
		input := fs.String("input", "", "one JSONL file (default: all system outputs)")
		_ = fs.Parse(os.Args[2:])
		entry, root, err := pinnedSystem(lock, corpusDir, *system)
		if err != nil {
			fatal("%v", err)
		}
		paths := []string{*input}
		if *input == "" {
			paths, err = filepath.Glob(filepath.Join(outDir, entry.Name+".*.jsonl"))
			if err != nil {
				fatal("%v", err)
			}
		}
		if len(paths) == 0 {
			fatal("no outputs found for %s", entry.Name)
		}
		for _, path := range paths {
			facts, err := readFacts(path)
			if err != nil {
				fatal("%s: %v", path, err)
			}
			if err := verifyPinnedFacts(*entry, root, facts); err != nil {
				fatal("%s: %v", path, err)
			}
			fmt.Printf("%s: verified %d facts\n", path, len(facts))
		}
	case "stats":
		fs := flag.NewFlagSet("stats", flag.ExitOnError)
		system := fs.String("system", "", "corpus system name (required)")
		_ = fs.Parse(os.Args[2:])
		facts, err := readFacts(filepath.Join(outDir, *system+".facts.jsonl"))
		if err != nil {
			fatal("%v", err)
		}
		printStats(facts)
	default:
		fatal("unknown subcommand %q", os.Args[1])
	}
}

func readFacts(path string) (facts []Fact, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	dec := json.NewDecoder(f)
	for {
		var fact Fact
		if err := dec.Decode(&fact); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func pinnedSystem(lock, corpusDir, system string) (*CorpusEntry, string, error) {
	if system == "" {
		return nil, "", fmt.Errorf("-system required")
	}
	entries, err := loadCorpus(lock)
	if err != nil {
		return nil, "", err
	}
	for i := range entries {
		if entries[i].Name == system {
			root := filepath.Join(corpusDir, system)
			if err := verifyCorpus(root, entries[i].Commit); err != nil {
				return nil, "", fmt.Errorf("corpus %s: %w", system, err)
			}
			return &entries[i], root, nil
		}
	}
	return nil, "", fmt.Errorf("unknown system %q", system)
}

func extractionSnapshot(lock, corpusDir, system string) (*CorpusEntry, string, string, func(), error) {
	entry, root, err := pinnedSystem(lock, corpusDir, system)
	if err != nil {
		return nil, "", "", nil, err
	}
	snapshot, cleanup, err := snapshotCorpus(root, *entry)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("snapshot corpus %s: %w", system, err)
	}
	return entry, root, snapshot, cleanup, nil
}

func validateAndPublish(entry CorpusEntry, corpusRoot, snapshot, path string, facts []Fact) error {
	sortFacts(facts)
	if err := validateSnapshotFacts(entry, snapshot, facts); err != nil {
		return fmt.Errorf("validate extracted facts: %w", err)
	}
	if err := verifyCorpus(corpusRoot, entry.Commit); err != nil {
		return fmt.Errorf("corpus changed during extraction: %w", err)
	}
	if err := verifyPinnedFacts(entry, corpusRoot, facts); err != nil {
		return fmt.Errorf("validate facts against pinned Git objects: %w", err)
	}
	postWriteVerify := func() error { return verifyCorpus(corpusRoot, entry.Commit) }
	if err := writeFactsAtomic(path, facts, postWriteVerify); err != nil {
		return fmt.Errorf("publish facts: %w", err)
	}
	return nil
}

func sortFacts(facts []Fact) {
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartByte != b.StartByte {
			return a.StartByte < b.StartByte
		}
		if a.EndByte != b.EndByte {
			return a.EndByte < b.EndByte
		}
		if a.Predicate != b.Predicate {
			return a.Predicate < b.Predicate
		}
		if a.Object != b.Object {
			return a.Object < b.Object
		}
		return a.AtomID < b.AtomID
	})
}

type publishHooks struct {
	afterTempSync func() error
	afterRename   func() error
}

func writeFactsAtomic(path string, facts []Fact, postWriteVerify func() error) error {
	return writeFactsAtomicWithHooks(path, facts, publishHooks{
		afterTempSync: postWriteVerify,
		afterRename:   postWriteVerify,
	})
}

func writeFactsAtomicWithHooks(path string, facts []Fact, hooks publishHooks) (retErr error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := secureMkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(path, true); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("acquire publish lock %s: %w", lockPath, err)
	}
	if _, err := fmt.Fprintf(lock, "pid=%d\n", os.Getpid()); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return fmt.Errorf("write publish lock: %w", err)
	}
	if err := lock.Sync(); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return fmt.Errorf("sync publish lock: %w", err)
	}
	if err := lock.Close(); err != nil {
		_ = os.Remove(lockPath)
		return fmt.Errorf("close publish lock: %w", err)
	}
	defer func() { _ = os.Remove(lockPath) }()

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	enc := json.NewEncoder(tmp)
	for _, fact := range facts {
		if err := enc.Encode(fact); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if hooks.afterTempSync != nil {
		if err := hooks.afterTempSync(); err != nil {
			return fmt.Errorf("post-write verification: %w", err)
		}
	}

	backupPath, hadPrior, err := backupArtifact(path)
	if err != nil {
		return err
	}
	if backupPath != "" {
		defer func() { _ = os.Remove(backupPath) }()
	}
	if err := rejectSymlinkComponents(path, true); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	renamed := true
	rollback := func(cause error) error {
		if !renamed {
			return cause
		}
		var restoreErr error
		if hadPrior {
			restoreErr = os.Rename(backupPath, path)
			if restoreErr == nil {
				backupPath = ""
			}
		} else {
			restoreErr = os.Remove(path)
		}
		if syncErr := syncDirectory(dir); syncErr != nil {
			restoreErr = errors.Join(restoreErr, syncErr)
		}
		if restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore prior artifact: %w", restoreErr))
		}
		return cause
	}
	if hooks.afterRename != nil {
		if err := hooks.afterRename(); err != nil {
			return rollback(fmt.Errorf("post-rename verification: %w", err))
		}
	}
	if err := syncDirectory(dir); err != nil {
		return rollback(fmt.Errorf("sync output directory: %w", err))
	}
	renamed = false
	if backupPath != "" {
		_ = os.Remove(backupPath)
		backupPath = ""
		_ = syncDirectory(dir)
	}
	return nil
}

func secureMkdirAll(path string, mode os.FileMode) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	rel := strings.TrimPrefix(abs, current)
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, mode); err != nil {
				return fmt.Errorf("create output directory %s: %w", current, err)
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("output directory component %s is not a real directory", current)
		}
	}
	return nil
}

func rejectSymlinkComponents(path string, finalMayNotExist bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	rel := strings.TrimPrefix(abs, current)
	components := strings.Split(rel, string(filepath.Separator))
	for i, component := range components {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && finalMayNotExist && i == len(components)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output path component %s is a symlink", current)
		}
		if i < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("output path component %s is not a directory", current)
		}
		if i == len(components)-1 && info.IsDir() {
			return fmt.Errorf("output path %s is a directory", current)
		}
	}
	return nil
}

func backupArtifact(path string) (backupPath string, existed bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("existing output %s is not a regular file", path)
	}
	source, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			_ = source.Close()
		}
	}()
	backup, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".previous-*")
	if err != nil {
		return "", false, err
	}
	backupPath = backup.Name()
	ok := false
	defer func() {
		if !ok {
			_ = backup.Close()
			_ = os.Remove(backupPath)
		}
	}()
	if err := backup.Chmod(info.Mode().Perm()); err != nil {
		return "", false, err
	}
	if _, err := io.Copy(backup, source); err != nil {
		return "", false, err
	}
	if err := source.Close(); err != nil {
		return "", false, err
	}
	sourceClosed = true
	if err := backup.Sync(); err != nil {
		return "", false, err
	}
	if err := backup.Close(); err != nil {
		return "", false, err
	}
	ok = true
	return backupPath, true, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func printStats(facts []Fact) {
	type key struct{ pred, role, tier string }
	counts := map[key]int{}
	for _, f := range facts {
		counts[key{f.Predicate, f.CodeRole, f.Tier}]++
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.pred != b.pred {
			return a.pred < b.pred
		}
		if a.role != b.role {
			return a.role < b.role
		}
		return a.tier < b.tier
	})
	for _, k := range keys {
		fmt.Printf("  %-22s %-11s %-10s %6d\n", k.pred, k.role, k.tier, counts[k])
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
