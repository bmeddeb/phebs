//go:build t472replay

// Offline, read-only replay of the Caller Map leaf lane (T47.2o). It re-runs
// the production direct scanner over every source file each retained leaf
// artifact records as scanned, with the same compiled resolver, and refuses
// unless every file's facts are byte-identical and the per-pair read counts
// and bytes equal the retained receipts. Build only through
// capture_replay.prepare_leaf_overlay with -tags t472replay.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// control is one repository's live Caller Map generation, as the product API
// reported it before export.
type control struct {
	Repository             string `json:"repository"`
	GenerationDigest       string `json:"generation_digest"`
	ManifestDigest         string `json:"manifest_digest"`
	ResolverManifestDigest string `json:"resolver_manifest_digest"`
}

type pairSummary struct {
	Repository   string `json:"repository"`
	Artifact     string `json:"artifact"`
	ScannedFiles int    `json:"scanned_files"`
	ScannedBytes int64  `json:"scanned_bytes"`
	Facts        int    `json:"facts"`
	Visits       int    `json:"visits"`
}

const maxLeafBytes = 64 << 20

func main() {
	data := flag.String("data", "", "absolute run data directory")
	controls := flag.String("controls", "", "JSON list of generation controls")
	out := flag.String("out", "", "new private output directory")
	flag.Parse()
	if err := run(context.Background(), *data, *controls, *out); err != nil {
		fmt.Fprintln(os.Stderr, "leaf replay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, data, controlsPath, out string) error {
	if !filepath.IsAbs(data) || !filepath.IsAbs(out) {
		return errors.New("data and out must be absolute")
	}
	raw, err := os.ReadFile(controlsPath)
	if err != nil {
		return err
	}
	var selected []control
	if err := json.Unmarshal(raw, &selected); err != nil || len(selected) == 0 {
		return errors.Join(errors.New("controls are invalid"), err)
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		return err
	}
	traceFile, err := os.OpenFile(filepath.Join(out, "leaf-trace.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer traceFile.Close()
	trace := bufio.NewWriter(traceFile)
	encoder := json.NewEncoder(trace)
	var summaries []pairSummary
	for _, current := range selected {
		pairs, err := replayRepository(ctx, data, current, encoder)
		if err != nil {
			return fmt.Errorf("%s: %w", current.Repository, err)
		}
		summaries = append(summaries, pairs...)
	}
	if err := trace.Flush(); err != nil {
		return err
	}
	if err := traceFile.Sync(); err != nil {
		return err
	}
	summary, err := json.MarshalIndent(map[string]any{
		"schema": "t472-leaf-replay-summary-v1", "pairs": summaries,
		"facts_byte_identical": true, "reads_match_receipts": true,
	}, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "leaf-replay-summary.json"), append(summary, '\n'), 0o600)
}

func replayRepository(ctx context.Context, data string, current control, trace *json.Encoder) ([]pairSummary, error) {
	overlay := filepath.Join(data, "caller-leaves", callerleafid.RepositoryDirectory(current.Repository))
	manifestPath := filepath.Join(overlay, "complete-v1-"+trimDigest(current.GenerationDigest)+"-"+trimDigest(current.ManifestDigest)+".manifest.json")
	var manifest callerpublication.Manifest
	if err := decodeFile(manifestPath, &manifest); err != nil {
		return nil, err
	}
	if err := callerpublication.ValidateManifest(manifest); err != nil {
		return nil, err
	}
	if manifest.Digest != current.ManifestDigest || manifest.Generation.Digest != current.GenerationDigest ||
		manifest.Generation.Repository != current.Repository ||
		manifest.Generation.ResolverManifestDigest != current.ResolverManifestDigest {
		return nil, errors.New("complete publication differs from the live generation control")
	}
	resolverRoot := filepath.Join(data, "resolver-catalogs")
	var resolverManifest resolvercatalog.Manifest
	if err := decodeFile(filepath.Join(resolverRoot, resolvercatalogid.ManifestName(current.Repository)), &resolverManifest); err != nil {
		return nil, err
	}
	state := resolverManifest.State()
	// The caller generation's resolver_manifest_digest is the resolver authority
	// digest (callerexecute/model.go), not the manifest file digest.
	if state.AuthorityDigest == "" || state.AuthorityDigest != current.ResolverManifestDigest ||
		state.GenerationDigest != manifest.Generation.ResolverGenerationDigest {
		return nil, errors.New("resolver catalog differs from the generation's resolver manifest")
	}
	_, view, err := resolvermaterialize.OpenCallerResolver(ctx, resolverRoot, state, resolvermaterialize.ProtocolGRPC)
	if err != nil {
		return nil, err
	}
	repositoryDir, err := phebssync.SafeRepoDir(data, current.Repository)
	if err != nil {
		return nil, err
	}
	var summaries []pairSummary
	for _, pair := range manifest.Pairs {
		if pair.Pair.Domain != "grpc-caller" {
			return nil, errors.New("unexpected caller domain")
		}
		summary, err := replayPair(ctx, overlay, repositoryDir, current, pair.Receipt, view.Resolver(), trace)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pair.Receipt.Name, err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func replayPair(ctx context.Context, overlay, repositoryDir string, current control, receipt callerleaf.Receipt,
	resolver *gocaller.DirectResolver, trace *json.Encoder) (pairSummary, error) {
	raw, err := os.ReadFile(filepath.Join(overlay, receipt.Name))
	if err != nil {
		return pairSummary{}, err
	}
	sum := sha256.Sum256(raw)
	if len(raw) > maxLeafBytes || "sha256:"+hex.EncodeToString(sum[:]) != receipt.ContentDigest {
		return pairSummary{}, errors.New("leaf artifact differs from its receipt")
	}
	type scanned struct {
		objectID string
		facts    []sdk.Fact
		noDirect bool
	}
	var order []string
	files := map[string]*scanned{}
	for _, line := range bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n")) {
		var record callerleaf.Record
		if err := json.Unmarshal(line, &record); err != nil {
			return pairSummary{}, err
		}
		if record.Coverage != nil {
			return pairSummary{}, errors.New("compact coverage hides the scanned-file set")
		}
		if record.Fact == nil && record.Reason != callerleaf.AbstentionReasonNoDirectCaller {
			continue // input abstentions are never scanned
		}
		file := files[record.Path]
		if file == nil {
			file = &scanned{objectID: record.ObjectID}
			files[record.Path] = file
			order = append(order, record.Path)
		}
		if file.objectID != record.ObjectID || file.noDirect || record.Fact == nil && len(file.facts) != 0 {
			return pairSummary{}, fmt.Errorf("%q has inconsistent leaf records", record.Path)
		}
		if record.Fact == nil {
			file.noDirect = true
		} else {
			file.facts = append(file.facts, *record.Fact)
		}
	}
	summary := pairSummary{Repository: current.Repository, Artifact: receipt.Name, ScannedFiles: len(order)}
	for _, path := range order {
		file := files[path]
		content, err := gitobj.ReadBlob(ctx, repositoryDir, file.objectID, callerleaf.MaxDirectSourceBytes)
		if err != nil {
			return pairSummary{}, err
		}
		if !utf8.Valid(content) {
			return pairSummary{}, fmt.Errorf("%q was recorded as scanned but is not UTF-8", path)
		}
		digest := sha256.Sum256(content)
		var replayed []sdk.Fact
		_, err = gocaller.ScanDirectSyntaxFile(ctx, gocaller.DirectScanRequest{
			Protocol: "grpc", Path: path,
			Blob:     sdk.Blob{Content: string(content), Digest: "sha256:" + hex.EncodeToString(digest[:])},
			Resolver: resolver, ResolverCatalogDigest: current.ResolverManifestDigest,
		}, func(fact sdk.Fact) error {
			replayed = append(replayed, fact)
			return nil
		})
		visits, observeErr := gocaller.T472Drain()
		if err = errors.Join(err, observeErr); err != nil {
			return pairSummary{}, fmt.Errorf("replay %q: %w", path, err)
		}
		want, err := json.Marshal(file.facts)
		if err != nil {
			return pairSummary{}, err
		}
		got, err := json.Marshal(replayed)
		if err != nil {
			return pairSummary{}, err
		}
		if file.noDirect && len(replayed) != 0 || !file.noDirect && !bytes.Equal(want, got) {
			return pairSummary{}, fmt.Errorf("%q replayed facts differ from the retained leaf", path)
		}
		for _, visit := range visits {
			if visit.Path != path {
				return pairSummary{}, errors.New("visit escaped its scanned file")
			}
			if err := trace.Encode(map[string]any{"repository": current.Repository, "path": path,
				"start_byte": visit.Start, "end_byte": visit.End, "lane": "caller-leaf-direct-syntax"}); err != nil {
				return pairSummary{}, err
			}
		}
		summary.ScannedBytes += int64(len(content))
		summary.Facts += len(replayed)
		summary.Visits += len(visits)
	}
	if summary.ScannedFiles != receipt.SourceBlobReads || summary.ScannedBytes != receipt.SourceBlobBytes {
		return pairSummary{}, errors.New("replayed reads differ from the retained receipt")
	}
	return summary, nil
}

func decodeFile(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func trimDigest(value string) string {
	if len(value) > len("sha256:") {
		return value[len("sha256:"):]
	}
	return value
}
