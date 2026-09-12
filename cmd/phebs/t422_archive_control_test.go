package main

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func t422ArchiveInputFixture() t422ArchiveInput {
	return t422ArchiveInput{BackupRoot: "t422-backup-1234", Device: 1, Inode: 2, FSID: [2]int32{3, 4},
		BackupCommandSHA256: "sha256:" + strings.Repeat("a", 64), RestoreCommandSHA256: "sha256:" + strings.Repeat("a", 64)}
}

func TestT422ArchiveClosedInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		change   func(*t422SemanticLaunchRequest)
		producer uint32
		phase    uint32
		want     bool
	}{
		{"complete", func(*t422SemanticLaunchRequest) {}, 6, 12, true},
		{"omitted", func(r *t422SemanticLaunchRequest) { r.Archive = nil }, 6, 12, true},
		{"epoch", func(r *t422SemanticLaunchRequest) { r.ServerEpoch = 4 }, 5, 8, false},
		{"producer", func(*t422SemanticLaunchRequest) {}, 5, 12, false},
		{"phase", func(*t422SemanticLaunchRequest) {}, 6, 13, false},
		{"absolute", func(r *t422SemanticLaunchRequest) { r.Archive.BackupRoot = "/tmp/t422-backup-1" }, 6, 12, false},
		{"traversal", func(r *t422SemanticLaunchRequest) { r.Archive.BackupRoot = "t422-backup-1/.." }, 6, 12, false},
		{"empty-nonce", func(r *t422SemanticLaunchRequest) { r.Archive.BackupRoot = "t422-backup-" }, 6, 12, false},
		{"oversize", func(r *t422SemanticLaunchRequest) { r.Archive.BackupRoot = "t422-backup-" + strings.Repeat("1", 128) }, 6, 12, false},
		{"inode", func(r *t422SemanticLaunchRequest) { r.Archive.Inode = 0 }, 6, 12, false},
		{"volume", func(r *t422SemanticLaunchRequest) { r.Archive.FSID = [2]int32{} }, 6, 12, false},
		{"different-digest", func(r *t422SemanticLaunchRequest) {
			r.Archive.RestoreCommandSHA256 = "sha256:" + strings.Repeat("b", 64)
		}, 6, 12, false},
		{"invalid-digest", func(r *t422SemanticLaunchRequest) {
			r.Archive.BackupCommandSHA256 = "invalid"
			r.Archive.RestoreCommandSHA256 = "invalid"
		}, 6, 12, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, snapshot := t422SemanticTestRequest(t)
			var request t422SemanticLaunchRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			input := t422ArchiveInputFixture()
			request.ServerEpoch, request.Archive = 5, &input
			test.change(&request)
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			snapshot.ProducerID, snapshot.Phase, snapshot.InputSHA256 = test.producer, test.phase, sha256.Sum256(raw)
			if _, err := decodeT422SemanticLaunch(raw, snapshot); (err == nil) != test.want {
				t.Fatal("closed archive input", err)
			}
		})
	}
	raw, _ := t422SemanticTestRequest(t)
	if strings.Contains(string(raw), `"archive"`) {
		t.Fatal("omitted historical bytes changed")
	}
}

func TestT422ArchiveClosedRoute(t *testing.T) {
	launch := &t422SemanticLaunch{}
	state := &t421ExactReadAccountingState{semantic: launch, archive: &t422ArchiveControl{launch: launch}}
	for _, test := range []struct {
		name, method, path string
		want               bool
	}{
		{"complete", http.MethodGet, t422ArchiveTransitionPath, true},
		{"post", http.MethodPost, t422ArchiveTransitionPath, false},
		{"query", http.MethodGet, t422ArchiveTransitionPath + "?path=/tmp/x", false},
		{"empty-query", http.MethodGet, t422ArchiveTransitionPath + "?", false},
		{"alias", http.MethodGet, "/api/t422/%61rchive/transition", false},
		{"other", http.MethodGet, "/api/t422/archive/other", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			r.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
			r.Header.Set(t421ExactReadOrdinalHeader, "1")
			if (state.archiveRead(r) != nil) != test.want {
				t.Fatal("archive route shape differs")
			}
			if test.want && !t422SemanticRequestRoute(r) {
				t.Fatal("native route absent from semantic admission")
			}
		})
	}
}
