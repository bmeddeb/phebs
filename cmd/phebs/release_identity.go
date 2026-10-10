package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"sort"

	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// releaseFacts is the machine-derived build identity of the running binary,
// read from its own embedded Go build metadata and executable file. It is the
// loading side's answer to a record's implementation block: none of these
// values is operator-declared or self-asserted by a release.
type releaseFacts struct {
	executable   string
	binaryDigest string
	goVersion    string
	sourceCommit string
}

// deriveReleaseLoadBindings is the production releaseLoadBindings: it derives
// what a released record must match from present fact rather than from
// configuration. It runs once per admitted startup, and only when the operator
// configured an artifact directory: without one the selection stays unbound on
// purpose, so a governing released record refuses startup (unresolved_reference)
// instead of loading without a binding.
//
// It performs no cancellation-dependent work: the census and the executable
// digest are each one bounded pass, and it never runs on a request, sync, or
// per-query path.
func deriveReleaseLoadBindings(_ context.Context, artifactsPath string) (packrelease.Options, error) {
	if artifactsPath == "" {
		return packrelease.Options{}, nil
	}
	facts, err := readReleaseBuildFacts()
	if err != nil {
		return packrelease.Options{}, fmt.Errorf("release implementation identity: %w", err)
	}
	return releaseLoadBindingsFor(artifactsPath, facts)
}

// readReleaseBuildFacts reads the ambient facts of the running binary: its
// embedded build metadata and its executable path.
func readReleaseBuildFacts() (releaseFacts, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return releaseFacts{}, fmt.Errorf("read embedded build metadata")
	}
	executable, err := os.Executable()
	if err != nil {
		return releaseFacts{}, fmt.Errorf("resolve running executable: %w", err)
	}
	facts, err := buildFacts(info, executable)
	if err != nil {
		return releaseFacts{}, err
	}
	facts.binaryDigest, err = executableidentity.RunningDigest()
	if err != nil {
		return releaseFacts{}, fmt.Errorf("digest running executable: %w", err)
	}
	return facts, nil
}

// buildFacts validates embedded build metadata and derives the release facts.
// It is fail closed: a binary that cannot state the exact commit it was built
// from, or that was built from a modified working tree, cannot bind any
// released record and refuses rather than naming an approximate identity. A
// plain toolchain version is not an identity, so it only ships beside one.
func buildFacts(info *debug.BuildInfo, executable string) (releaseFacts, error) {
	if info == nil {
		return releaseFacts{}, fmt.Errorf("no build metadata is embedded in this binary")
	}
	if info.GoVersion == "" {
		return releaseFacts{}, fmt.Errorf("build metadata names no Go toolchain version")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, duplicate := settings[setting.Key]; duplicate {
			return releaseFacts{}, fmt.Errorf("build metadata holds duplicate setting %q", setting.Key)
		}
		settings[setting.Key] = setting.Value
	}
	commit := settings["vcs.revision"]
	if !validSourceCommit(commit) {
		return releaseFacts{}, fmt.Errorf(
			"build metadata names no 40-hex lowercase vcs.revision, got %q", commit)
	}
	if settings["vcs.modified"] == "true" {
		return releaseFacts{}, fmt.Errorf(
			"this binary was built from a modified working tree, so it names no exact commit")
	}
	if executable == "" {
		return releaseFacts{}, fmt.Errorf("the running executable path is empty")
	}
	return releaseFacts{executable: executable, goVersion: info.GoVersion, sourceCommit: commit}, nil
}

// validSourceCommit mirrors the record-side commit grammar: a release's
// phebs_source_commit is exactly 40 lowercase hex characters.
func validSourceCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

// releaseLoadBindingsFor derives the load bindings from the running binary's
// facts and one census of the configured artifact directory. The
// implementation identity follows from the executable and the in-tree recipe
// registry, and the referenced-artifacts root and resolver follow from the
// bytes actually present, so every binding a released record must match comes
// from present fact rather than from the record or from configuration.
func releaseLoadBindingsFor(artifactsPath string, facts releaseFacts) (packrelease.Options, error) {
	binaryDigest := facts.binaryDigest
	if binaryDigest == "" {
		// Synthetic test facts bind a fixture executable. Production always
		// supplies the descriptor-derived running-image digest above.
		var err error
		binaryDigest, err = executableidentity.Digest(facts.executable)
		if err != nil {
			return packrelease.Options{}, fmt.Errorf("running executable identity: %w", err)
		}
	}
	implementation := packrelease.Implementation{
		PhebsSourceCommit:        facts.sourceCommit,
		PhebsBinaryDigest:        binaryDigest,
		PackImplementationDigest: packImplementationDigest(packRecipes),
		ToolchainDigest:          toolchainDigest(facts.goVersion),
	}
	directory, err := packrelease.OpenArtifactDirectory(artifactsPath)
	if err != nil {
		return packrelease.Options{}, err
	}
	return packrelease.Options{
		Implementation:                &implementation,
		ReferencedArtifactsRootDigest: directory.RootDigest(),
		Resolver:                      directory,
	}, nil
}

// packImplementationDigest is the content identity of the in-tree recipe
// registry: every pack this binary could implement, paired with the extractor
// domain@version set its recipe contributes, as globally sorted rows. The
// empty registry digests zero rows, which every release must then name
// explicitly. A registry binding or dropping a recipe changes the digest, so a
// release signed for a different implementation set cannot load this binary.
func packImplementationDigest(recipes map[string]func() []extract.Extractor) string {
	rows := make([]string, 0, len(recipes))
	for packID, recipe := range recipes {
		for _, extractor := range recipe() {
			rows = append(rows, packID+" "+extractor.Domain()+"@"+extractor.Version())
		}
	}
	sort.Strings(rows)
	var payload []byte
	for _, row := range rows {
		payload = append(payload, row...)
		payload = append(payload, '\n')
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// toolchainDigest names the Go toolchain the binary was built with. The plain
// go version string is not itself closed digest syntax, so the record carries
// its derived digest instead of an asserted version.
func toolchainDigest(goVersion string) string {
	sum := sha256.Sum256([]byte("go-toolchain " + goVersion + "\n"))
	return "sha256:" + hex.EncodeToString(sum[:])
}
