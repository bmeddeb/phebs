//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// Test-owned prerequisite, before the selected child/FD6 bootstrap. This is
// actual whole-search publication, not selected phase-six Zoekt allowance.
// The same original fixture deadline covers its build, index, source and join.
func t422MarkerNativeIndex(t *testing.T, ctx context.Context, root, mirror, repository, commit string) {
	t.Helper()
	module, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "marker-zoekt-git-index")
	t421BlackBoxBuild(t, ctx, module, bin, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	t421BlackBoxRun(t, ctx, "", "git", "--git-dir", mirror, "config", "zoekt.name", repository)
	shards, sourceStage, indexRoot := filepath.Join(root, "marker-shards"), filepath.Join(root, "marker-source"), filepath.Join(root, "data", "index")
	for _, dir := range []string{shards, indexRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.CommandContext(ctx, bin, "-index", shards, "-incremental=false", "-submodules=false",
		"-file_limit=2097152", "-shard_limit=104857600", "-max_trigram_count=20000", mirror)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "ZOEKT_DISABLE_CATFILE_BATCH" && !strings.HasPrefix(key, "PHEBS_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "ZOEKT_DISABLE_CATFILE_BATCH=true")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Cancel = func() error { return t4013.KillPrivateProcessSession(command.Process.Pid) }
	log, err := os.OpenFile(filepath.Join(root, "marker-index-setup.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	waitErr := command.Wait()
	closeErr := log.Close()
	deadline, bounded := ctx.Deadline()
	if !bounded || ctx.Err() != nil {
		t.Fatal("original indexing deadline", ctx.Err())
	}
	joinErr := t4013.WaitPrivateProcessSession(command.Process.Pid, deadline)
	if err := errors.Join(waitErr, closeErr, joinErr, ctx.Err()); err != nil {
		t.Fatal("actual setup index/session", err)
	}
	revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}}
	source, err := repositoryindex.BuildSourceGeneration(ctx, mirror, sourceStage, repository, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.PublishWholeGeneration(ctx, indexRoot, shards, sourceStage, repository, revisions, source); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.FinishPublication(indexRoot, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := focusedindex.ValidateRepositorySearchGeneration(ctx, indexRoot, repository, revisions); err != nil {
		t.Fatal("actual setup-owned immutable search publication", err)
	}
}

// One actual stderr writer, one fixed line buffer, two scalar samples. The
// existing retained diagnostic owns the bytes; the tap reads no buffer before
// Wait. Unlike the partial warm tap it rejects any extra WB record after finish.
type t422MarkerNativeOutput struct {
	sink    io.Writer
	input   [32]byte
	mu      sync.Mutex
	line    [79]byte
	length  int
	record  int
	armed   bool
	err     error
	samples [2]t422WorkspaceSampleResponse
	ready   chan t422WorkspaceSampleResponse
}

func (out *t422MarkerNativeOutput) arm(t *testing.T) {
	t.Helper()
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.armed || out.record != 1 || out.err != nil {
		t.Fatal("marker tap must arm after binding and before HIT")
	}
	out.armed = true
}

func (out *t422MarkerNativeOutput) Write(raw []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.err != nil {
		return 0, out.err
	}
	n, err := out.sink.Write(raw)
	if err != nil {
		out.err = err
		return n, err
	}
	for _, b := range raw[:n] {
		if b != '\n' {
			if out.length < len(out.line) {
				out.line[out.length] = b
			}
			if out.length <= len(out.line) {
				out.length++
			}
			continue
		}
		length := out.length
		out.length = 0
		if length < 2 || out.line[0] != 'W' || out.line[1] != 'B' {
			continue
		}
		line := string(out.line[:min(length, len(out.line))])
		valid := length < len(out.line)
		switch out.record {
		case 0:
			valid = valid && line == fmt.Sprintf("WBB1:4:sha256:%x", out.input)
		case 1, 4:
			sequence := uint64(1)
			if out.record == 4 {
				sequence = 2
			}
			valid = valid && out.armed && line == fmt.Sprintf("WB1:4:6B:%016x", sequence)
		case 2, 5:
			index := 0
			if out.record == 5 {
				index = 1
			}
			sequence := uint64(index + 1)
			var seq, logical, allocated uint64
			count, scanErr := fmt.Sscanf(line, "WB1:4:6S:%016x:%016x:%016x", &seq, &logical, &allocated)
			valid = valid && count == 3 && scanErr == nil && seq == sequence && logical >= 1<<20 && allocated > 0 &&
				line == fmt.Sprintf("WB1:4:6S:%016x:%016x:%016x", sequence, logical, allocated)
			if valid {
				out.samples[index] = t422WorkspaceSampleResponse{LogicalBytes: logical, AllocatedBytes: allocated}
			}
		case 3:
			valid = valid && line == "WB1:4:6R:0000000000000001"
		default:
			valid = false
		}
		if !valid {
			out.err = errors.New("native marker WB prefix refused")
			select {
			case out.ready <- t422WorkspaceSampleResponse{}:
			default:
			}
			return n, out.err
		}
		out.record++
		if out.record == 4 {
			out.ready <- out.samples[0]
		}
	}
	return n, nil
}

func (out *t422MarkerNativeOutput) assertJoined(t *testing.T, finish t422WorkspaceSampleResponse) {
	t.Helper()
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.err != nil || out.record != 6 || out.length != 0 || out.samples[1] != finish {
		t.Fatal("joined marker/finish WB exact values", out.record, out.err)
	}
}

func t422MarkerNativeParent(t *testing.T, ctx context.Context, endpoint string, control *dispatchadmission.PhaseControl,
	input io.Writer, output *bufio.Scanner, tap *t422MarkerNativeOutput) t422WorkspaceSampleResponse {
	t.Helper()
	// Bootstrap starts in the real open owner/request state. ReopenOwners
	// is only legal after Resume; do not fabricate that transition here.
	if control.RequestToken() == "" {
		t.Fatal("actual initial marker request window")
	}
	if _, err := fmt.Fprintln(input, "start"); err != nil || !output.Scan() || output.Text() != "marker_running" {
		t.Fatal("actual marker scheduler start", err, output.Err())
	}
	tap.arm(t)
	client := &http.Client{}
	defer client.CloseIdleConnections()
	request := func(path string, ordinal uint64) t422MarkerObservation {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		req.Header.Set(dispatchadmission.ProductionRequestHeader, control.RequestToken())
		req.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
		req.Header.Set(t421ExactReadOrdinalHeader, strconv.FormatUint(ordinal, 10))
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("actual marker HTTP", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		closeErr := response.Body.Close()
		var value t422MarkerObservation
		if response.StatusCode != http.StatusOK || len(raw) > 64<<10 || readErr != nil || closeErr != nil || json.Unmarshal(raw, &value) != nil {
			t.Fatal("actual marker response refusal", response.StatusCode, readErr, closeErr)
		}
		encoded := response.Trailer.Get(t421ExactReadTrailer)
		reportRaw, err := base64.RawURLEncoding.DecodeString(encoded)
		var report t421ExactReadReport
		if err != nil || len(encoded) > 1024 || json.Unmarshal(reportRaw, &report) != nil ||
			report.Schema != t421ExactReadReportSchema || report.Status != "complete" || report.RequestOrdinal != ordinal ||
			report.ControlFileReads != relationshippublication.PublicationTransitionControlFileReadsV3 ||
			report.StoreReadAttempts != 0 || report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			t.Fatal("actual marker exact read report", err)
		}
		if value.Schema != "t422-relationship-marker-observation-v3" || value.PriorGenerationDigest == "" ||
			value.PriorRootDigest == "" || value.TargetGenerationDigest == "" || value.TargetRootDigest == "" ||
			value.PriorGenerationDigest == value.TargetGenerationDigest || value.PriorRootDigest == value.TargetRootDigest {
			t.Fatal("actual prior/target transition absent")
		}
		return value
	}
	hit := request(t422MarkerHitPath, 1)
	if hit.Point != "hit" {
		t.Fatal("actual HIT point")
	}
	select {
	case <-ctx.Done():
		t.Fatal("original marker walk deadline", ctx.Err())
	case sample := <-tap.ready:
		if sample.LogicalBytes < 1<<20 || sample.AllocatedBytes == 0 {
			t.Fatal("actual guarded S and reopen R unavailable")
		}
	}
	recovered := request(t422MarkerRecoveredPath, 2)
	if recovered.Point != "recovered" {
		t.Fatal("actual RECOVERED point")
	}
	recovered.Point = hit.Point
	if recovered != hit {
		t.Fatal("actual marker recovery changed bound authority")
	}
	if !output.Scan() || output.Text() != "marker_complete" {
		t.Fatal("actual service Advance and one scheduler Complete", output.Err())
	}
	if control.DrainOwners(ctx) != nil || control.OpenRequests(ctx) != nil {
		t.Fatal("actual marker finish fence")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+t422WorkspaceSamplePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	req.Header.Set(dispatchadmission.ProductionRequestHeader, control.RequestToken())
	req.Header.Set(t422WorkspacePointHeader, "finish")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	closeErr := response.Body.Close()
	var finish t422WorkspaceSampleResponse
	if response.StatusCode != http.StatusOK || len(raw) > 64<<10 || readErr != nil || closeErr != nil ||
		json.Unmarshal(raw, &finish) != nil || finish.LogicalBytes < 1<<20 || finish.AllocatedBytes == 0 {
		t.Fatal("actual marker final workspace observation", response.StatusCode, readErr, closeErr)
	}
	if control.FenceRequests(ctx) != nil || control.Pause(ctx) != nil {
		t.Fatal("actual marker final fences")
	}
	if _, err := fmt.Fprintln(input, "close"); err != nil {
		t.Fatal(err)
	}
	return finish
}

// Supplied wire strings test only the new fixed parser state, not native
// publication, measurement or readiness. The opt-in fixture above owns those.
func TestT422MarkerNativeOutputModel(t *testing.T) {
	binding := fmt.Sprintf("WBB1:4:sha256:%x\n", [32]byte{1})
	begin := "WB1:4:6B:0000000000000001\n"
	sample := "WB1:4:6S:0000000000000001:0000000000100000:0000000000001000\n"
	ready := "WB1:4:6R:0000000000000001\n"
	finish := "WB1:4:6B:0000000000000002\nWB1:4:6S:0000000000000002:0000000000200000:0000000000002000\n"
	t.Run("S is not R", func(t *testing.T) {
		out := &t422MarkerNativeOutput{sink: io.Discard, input: [32]byte{1}, ready: make(chan t422WorkspaceSampleResponse, 1)}
		if _, err := out.Write([]byte(binding)); err != nil {
			t.Fatal(err)
		}
		out.arm(t)
		for _, b := range []byte(begin + sample) {
			if _, err := out.Write([]byte{b}); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-out.ready:
			t.Fatal("S fabricated readiness")
		default:
		}
		if _, err := out.Write([]byte(ready)); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-out.ready:
			if got.LogicalBytes != 1<<20 || got.AllocatedBytes != 4096 {
				t.Fatal(got)
			}
		default:
			t.Fatal("R did not release actual modeled prefix")
		}
		if _, err := out.Write([]byte(finish)); err != nil {
			t.Fatal(err)
		}
		out.assertJoined(t, t422WorkspaceSampleResponse{LogicalBytes: 2 << 20, AllocatedBytes: 8192})
		if len(binding+begin+sample+ready+finish) != 277 {
			t.Fatal("two-pair plus R framing")
		}
	})
	for _, tc := range []struct{ name, prefix string }{
		{"wrong producer", strings.Replace(begin, "WB1:4:", "WB1:3:", 1)},
		{"missing begin", sample},
		{"R before S", begin + ready},
		{"wrong sequence", begin + strings.Replace(sample, "0000000000000001", "0000000000000002", 1)},
		{"extra WB", begin + sample + ready + finish + ready},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &t422MarkerNativeOutput{sink: io.Discard, input: [32]byte{1}, ready: make(chan t422WorkspaceSampleResponse, 1)}
			if _, err := out.Write([]byte(binding)); err != nil {
				t.Fatal(err)
			}
			out.arm(t)
			if _, err := out.Write([]byte(tc.prefix)); err == nil || out.err == nil {
				t.Fatal("invalid modeled wire accepted")
			}
		})
	}
}
