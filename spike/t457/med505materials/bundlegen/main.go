// Command bundlegen writes the canonical prehydration inventory and amd64 typed
// profile for an assembled MED-505 input bundle. It reads the bundle tree only:
// it refuses links and non-regular files, and both outputs are round-tripped
// through typedindex.DecodeInventory and DecodeProfile before they are written.
//
// Usage:
//
//	bundlegen -bundle DIR -image sha256:... -out DIR
//	bundlegen -final -in DIR -helper FILE -mkfs sha256:... -image sha256:... -out DIR
//
// The -final mode rebuilds the bytes that the native preparation post-run chain
// seals only as digests: typed-bazel-selection.json, typed-host-tools.json and the
// FINAL inventory and profile. It reads config.json, result.json, inventory.json and
// provenance.json from -in, and uses provider.BuildSelection and the same canonical
// encoders as that chain, so the outputs must reproduce postrun.json's digests.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const profileName = "native-linux-amd64-rules-go-059-v1"

func main() {
	bundle := flag.String("bundle", "", "assembled bundle directory")
	image := flag.String("image", "", "runtime image ID (sha256:...)")
	out := flag.String("out", "", "output directory for inventory.json and profile.json")
	final := flag.Bool("final", false, "rebuild the post-run final selection, inventory and profile")
	in := flag.String("in", "", "-final: post-run input directory")
	helper := flag.String("helper", "", "-final: final cmd helper binary")
	mkfs := flag.String("mkfs", "", "-final: formatter digest")
	flag.Parse()
	if *final {
		if err := runFinal(context.Background(), *in, *helper, *mkfs, *image, *out); err != nil {
			fmt.Fprintln(os.Stderr, "bundlegen:", err)
			os.Exit(1)
		}
		return
	}
	if *bundle == "" || *image == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: bundlegen -bundle DIR -image sha256:... -out DIR")
		os.Exit(2)
	}
	if err := run(context.Background(), *bundle, *image, *out); err != nil {
		fmt.Fprintln(os.Stderr, "bundlegen:", err)
		os.Exit(1)
	}
}

func fileDigest(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, err
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Inventory walks the bundle and returns its canonical inventory bytes.
func Inventory(root string) ([]byte, error) {
	var files []typedindex.BundleFile
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: not a regular file", name)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		sum, size, err := fileDigest(name)
		if err != nil {
			return err
		}
		files = append(files, typedindex.BundleFile{Path: filepath.ToSlash(rel), Bytes: size, Digest: sum, Executable: info.Mode().Perm()&0o111 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
}

// Profile returns the canonical amd64 profile for an inventory digest, image and helper.
func Profile(bundleDigest, image, helper string) ([]byte, error) {
	goSDK, scip, bazel, ok := provider.NativeToolDigests("amd64")
	if !ok {
		return nil, errors.New("amd64 tool pins unavailable")
	}
	d := typedindex.ProfileDefinition{
		Schema:   typedindex.Amd64ProfileSchema,
		Name:     profileName,
		Provider: typedindex.ProviderID,
		Tools: typedindex.Tools{
			Bazel:    typedindex.Tool{Version: "9.0.0", Digest: bazel},
			RulesGo:  typedindex.Tool{Version: "0.59.0", Digest: "sha256:68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86"},
			Go:       typedindex.Tool{Version: typedindex.NativeSDKVersion("amd64"), Digest: goSDK},
			Driver:   typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + launcher.NativeDriverDigest("amd64")},
			Indexer:  typedindex.Tool{Version: "0.2.7", Digest: scip},
			Planner:  typedindex.Tool{Version: "neutral", Digest: helper},
			Launcher: typedindex.Tool{Version: "neutral", Digest: helper},
		},
		Config:       typedindex.Amd64ReducedConfig(),
		Policy:       typedindex.MeasuredPolicy(),
		BundleDigest: bundleDigest,
		ImageDigest:  image,
	}
	return json.Marshal(d)
}

func run(ctx context.Context, bundle, image, out string) error {
	inv, err := Inventory(bundle)
	if err != nil {
		return err
	}
	decoded, err := typedindex.DecodeInventory(ctx, inv, digestOf(inv))
	if err != nil {
		return fmt.Errorf("inventory refused: %w", err)
	}
	helper, ok := decoded.File(typedindex.ManagedHelperFile)
	if !ok {
		return errors.New("bundle has no managed helper")
	}
	prof, err := Profile(decoded.Digest(), image, helper.Digest)
	if err != nil {
		return err
	}
	p, err := typedindex.DecodeProfile(ctx, prof)
	if err != nil {
		return fmt.Errorf("profile refused: %w", err)
	}
	for name, raw := range map[string][]byte{"inventory.json": inv, "profile.json": prof} {
		if err = os.WriteFile(filepath.Join(out, name), raw, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("inventory %s files=%d bytes=%d\nprofile %s\n", decoded.Digest(), len(decoded.Files()), decoded.Bytes(), p.Digest())
	return nil
}

// finalTools mirrors the post-run chain's pinned tool set: six frozen native
// identities, with the final cmd helper as planner and launcher.
func finalTools(helperDigest string) (typedindex.Tools, error) {
	goSDK, scip, bazel, ok := provider.NativeToolDigests("amd64")
	if !ok {
		return typedindex.Tools{}, errors.New("amd64 tool pins unavailable")
	}
	h := typedindex.Tool{Version: "cmd", Digest: helperDigest}
	return typedindex.Tools{
		Bazel:    typedindex.Tool{Version: "9.0.0", Digest: bazel},
		RulesGo:  typedindex.Tool{Version: "0.59.0", Digest: "sha256:68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86"},
		Go:       typedindex.Tool{Version: typedindex.NativeSDKVersion("amd64"), Digest: goSDK},
		Driver:   typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + launcher.NativeDriverDigest("amd64")},
		Indexer:  typedindex.Tool{Version: "0.2.7", Digest: scip},
		Planner:  h,
		Launcher: h,
	}, nil
}

func encodeInventory(files []typedindex.BundleFile) ([]byte, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
}

func runFinal(ctx context.Context, in, helperPath, mkfs, image, out string) error {
	if in == "" || helperPath == "" || mkfs == "" || image == "" || out == "" {
		return errors.New("-final needs -in, -helper, -mkfs, -image and -out")
	}
	var cfg struct {
		Root, Module, Remote string
		Source               typedindex.Source `json:"source"`
	}
	var result struct {
		Cquery      []byte            `json:"cquery"`
		Aquery      []byte            `json:"aquery"`
		Projections map[string][]byte `json:"projections"`
	}
	var prov struct {
		Source typedindex.Source `json:"source"`
	}
	for name, v := range map[string]any{"config.json": &cfg, "result.json": &result, "provenance.json": &prov} {
		raw, err := os.ReadFile(filepath.Join(in, name))
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if prov.Source != cfg.Source {
		return errors.New("provenance source differs from config")
	}
	invRaw, err := os.ReadFile(filepath.Join(in, "inventory.json"))
	if err != nil {
		return err
	}
	pre, err := typedindex.DecodeInventory(ctx, invRaw, digestOf(invRaw))
	if err != nil {
		return err
	}
	helperDigest, helperBytes, err := fileDigest(helperPath)
	if err != nil {
		return err
	}
	files := pre.Files()
	swapped := 0
	for i, f := range files {
		if f.Path == typedindex.ManagedHelperFile || f.Path == "tools/bin/phebs-t451b-native-driver" {
			files[i] = typedindex.BundleFile{Path: f.Path, Bytes: helperBytes, Digest: helperDigest, Executable: true}
			swapped++
		}
	}
	if swapped != 2 {
		return errors.New("pre-selection inventory lacks the two helper paths")
	}
	substitutedRaw, err := encodeInventory(files)
	if err != nil {
		return err
	}
	substituted, err := typedindex.DecodeInventory(ctx, substitutedRaw, digestOf(substitutedRaw))
	if err != nil {
		return err
	}
	selection, universe, err := provider.BuildSelection(ctx, provider.Provisioning{
		Source: cfg.Source, Inventory: substituted, Roots: []string{cfg.Root}, Module: cfg.Module, Remote: cfg.Remote,
		Cquery: result.Cquery, Aquery: result.Aquery, Projections: result.Projections,
	})
	if err != nil {
		return fmt.Errorf("selection: %w", err)
	}
	hostTools, err := json.Marshal(typedindex.HostToolsDefinition{Schema: typedindex.HostToolsSchema, MkfsDigest: mkfs})
	if err != nil {
		return err
	}
	files = append(substituted.Files(),
		typedindex.BundleFile{Path: provider.SelectionFile, Bytes: int64(len(selection)), Digest: digestOf(selection)},
		typedindex.BundleFile{Path: typedindex.HostToolsFile, Bytes: int64(len(hostTools)), Digest: digestOf(hostTools)})
	finalRaw, err := encodeInventory(files)
	if err != nil {
		return err
	}
	finalInv, err := typedindex.DecodeInventory(ctx, finalRaw, digestOf(finalRaw))
	if err != nil {
		return err
	}
	tools, err := finalTools(helperDigest)
	if err != nil {
		return err
	}
	schema, config := typedindex.HostReducedIdentity()
	profRaw, err := json.Marshal(typedindex.ProfileDefinition{
		Schema: schema, Name: "neutral", Provider: typedindex.ProviderID, Tools: tools, Config: config,
		Policy: typedindex.MeasuredPolicy(), BundleDigest: finalInv.Digest(), ImageDigest: image,
	})
	if err != nil {
		return err
	}
	prof, err := typedindex.DecodeProfile(ctx, profRaw)
	if err != nil {
		return err
	}
	for name, raw := range map[string][]byte{provider.SelectionFile: selection, typedindex.HostToolsFile: hostTools, "inventory.json": finalRaw, "profile.json": profRaw} {
		if err = os.WriteFile(filepath.Join(out, name), raw, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("selection %s\nuniverse %s\nfinal_inventory %s\nprofile %s\n", digestOf(selection), universe, finalInv.Digest(), prof.Digest())
	return nil
}
