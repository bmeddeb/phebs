package t201

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed index.lock.json testdata/index.scip
var preparedInputs embed.FS

type indexLock struct {
	Version  string `json:"version"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Preparer string `json:"preparer"`
	Policy   string `json:"policy"`
}

// Profile is a complete immutable synthetic repository view plus its
// candidate-independent oracle.
type Profile struct {
	Name   string
	Files  map[string][]byte
	Oracle Oracle
}

// GenerateProfile creates a fresh deterministic profile. It never invokes an
// indexer: the reviewed SCIP bytes are read from the embedded preparation
// artifact, verified, and copied verbatim.
func GenerateProfile(name string) (Profile, error) {
	index, err := pinnedIndex()
	if err != nil {
		return Profile{}, err
	}
	switch name {
	case SmallProfileName:
		files := cloneFiles(SmallSourceFiles())
		files["index.scip"] = slices.Clone(index)
		oracle, err := smallOracle(files)
		if err != nil {
			return Profile{}, err
		}
		return Profile{Name: name, Files: files, Oracle: oracle}, nil
	case ScaleProfileName:
		return scaleProfile(index)
	default:
		return Profile{}, fmt.Errorf("unknown T20.1 profile %q", name)
	}
}

func pinnedIndex() ([]byte, error) {
	lockBytes, err := preparedInputs.ReadFile("index.lock.json")
	if err != nil {
		return nil, fmt.Errorf("read SCIP lock: %w", err)
	}
	var lock indexLock
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		return nil, fmt.Errorf("decode SCIP lock: %w", err)
	}
	if lock.Version != "t20-scip-input-v1" ||
		lock.Path != "testdata/index.scip" ||
		lock.Policy != "prepared-once-reviewed-and-copied-verbatim" {
		return nil, errors.New("SCIP lock has an unsupported shape")
	}
	index, err := preparedInputs.ReadFile(lock.Path)
	if err != nil {
		return nil, fmt.Errorf("read pinned SCIP input: %w", err)
	}
	sum := sha256.Sum256(index)
	if hex.EncodeToString(sum[:]) != lock.SHA256 {
		return nil, errors.New("pinned SCIP input digest mismatch")
	}
	return index, nil
}

func scaleProfile(index []byte) (Profile, error) {
	files := cloneFiles(SmallSourceFiles())
	files["index.scip"] = slices.Clone(index)
	oracle, err := smallOracle(files)
	if err != nil {
		return Profile{}, err
	}
	oracle.Profile = ScaleProfileName
	for fileIndex := range ScaleCallFiles {
		relPath := fmt.Sprintf("src/unit_%03d/callers.go", fileIndex)
		var source strings.Builder
		source.WriteString("package unit\n")
		source.WriteString("type OrdersClient interface { Get(any, any, ...any) (any, error) }\n")
		for callIndex := range ScaleCallsPerFile {
			unit := fileIndex*ScaleCallsPerFile + callIndex
			fmt.Fprintf(&source,
				"func Call%05d(ctx any, client OrdersClient) { _, _ = client.Get(ctx, \"unit-%05d\") }\n",
				unit, unit)
		}
		content := []byte(source.String())
		files[relPath] = content
		for callIndex := range ScaleCallsPerFile {
			unit := fileIndex*ScaleCallsPerFile + callIndex
			id := fmt.Sprintf("scale-call-%05d", unit)
			call, err := locateCall(id, "grpc", "/synthetic.orders.v1.Orders/Get",
				relPath, content, "client.Get", callIndex, "production",
				ResolutionKnown, "scip_symbol_to_generated_anchor", ProtoGetSymbol)
			if err != nil {
				return Profile{}, err
			}
			oracle.Calls = append(oracle.Calls, call)
			oracle.UnitMappings = append(oracle.UnitMappings, UnitMappingExpectation{
				CallID: id, SourcePath: relPath, StartLine: call.StartLine,
				BuildTargets:   []string{fmt.Sprintf("//src/unit_%03d:lib", fileIndex)},
				Deployables:    []string{fmt.Sprintf("deployable-%05d", unit)},
				LogicalService: []string{fmt.Sprintf("service-%05d", unit)},
				Owners:         []string{fmt.Sprintf("team-%03d", unit%250)},
				State:          "resolved",
				Source:         "unit-snapshot-v1",
			})
		}
	}
	unitSnapshot, err := json.MarshalIndent(struct {
		Version  string                   `json:"version"`
		Mappings []UnitMappingExpectation `json:"mappings"`
	}{
		Version: "t20-unit-snapshot-v1", Mappings: oracle.UnitMappings,
	}, "", "  ")
	if err != nil {
		return Profile{}, fmt.Errorf("encode scale unit snapshot: %w", err)
	}
	files["unit-snapshot.json"] = append(unitSnapshot, '\n')

	repeated := []byte("package repeated\nconst ContentAtom = \"same-content\"\n")
	for placement := range RepeatedPlacements {
		files[fmt.Sprintf("copies/%03d/atom.go", placement)] = slices.Clone(repeated)
	}
	return Profile{Name: ScaleProfileName, Files: files, Oracle: oracle}, nil
}

// ManifestDigest binds every path and byte in sorted order. It is not a Git
// object identity; tests independently require equal Git tree identities.
func ManifestDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	h := sha256.New()
	_, _ = h.Write([]byte("phebs-t20-corpus-v1\x00"))
	var size [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(size[:], uint64(len(name)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(name))
		binary.BigEndian.PutUint64(size[:], uint64(len(files[name])))
		_, _ = h.Write(size[:])
		_, _ = h.Write(files[name])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// WriteProfile materializes a generated profile without executing any corpus
// content. The destination must already exist and be empty.
func WriteProfile(root string, profile Profile) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read destination: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("destination must be empty")
	}
	names := make([]string, 0, len(profile.Files))
	for name := range profile.Files {
		if !validProfilePath(name) {
			return fmt.Errorf("unsafe generated path %q", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", name, err)
		}
		if err := os.WriteFile(target, profile.Files[name], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func validProfilePath(name string) bool {
	return name != "" && filepath.IsLocal(filepath.FromSlash(name)) &&
		filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) == name &&
		!strings.Contains(name, `\`)
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, content := range files {
		out[name] = slices.Clone(content)
	}
	return out
}
