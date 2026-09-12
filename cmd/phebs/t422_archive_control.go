package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const t422ArchiveTransitionPath = "/api/t422/archive/transition"
const t422ArchiveTransitionBytes = 1 << 20

var errT422ArchiveControl = errors.New("T42.2 archive transition refused")

// The parent supplies the actual held backup-root identity and the digests
// returned by its joined native commands. Only the fixed archive/manifest.json
// beneath this single workspace leaf is readable; there is no path endpoint.
type t422ArchiveInput struct {
	BackupRoot           string   `json:"backup_root"`
	Device               uint64   `json:"device"`
	Inode                uint64   `json:"inode"`
	FSID                 [2]int32 `json:"fsid"`
	BackupCommandSHA256  string   `json:"backup_command_sha256"`
	RestoreCommandSHA256 string   `json:"restore_command_sha256"`
}

func validT422ArchiveInput(input t422ArchiveInput) bool {
	const prefix = "t422-backup-"
	if !strings.HasPrefix(input.BackupRoot, prefix) || len(input.BackupRoot) <= len(prefix) || len(input.BackupRoot) > 128 ||
		input.Inode == 0 || input.FSID == ([2]int32{}) || !t422SemanticDigest(input.BackupCommandSHA256) ||
		input.BackupCommandSHA256 != input.RestoreCommandSHA256 {
		return false
	}
	for _, c := range input.BackupRoot[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

type t422ArchiveControl struct {
	ctx      context.Context
	launch   *t422SemanticLaunch
	input    t422ArchiveInput
	custody  *t422ArchiveCustody
	mu       sync.Mutex // Serializes the one bounded read and owned-FD close.
	reading  bool
	reported bool
	closed   bool
	err      error
}

func newT422ArchiveControl(ctx context.Context, launch *t422SemanticLaunch) (*t422ArchiveControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || launch.request.Archive == nil ||
		launch.request.ServerEpoch != 5 || launch.initial.ProducerID != 6 || launch.initial.Phase != 12 || err != nil ||
		!launch.matches(current) || current.Phase != 12 || !validT422ArchiveInput(*launch.request.Archive) {
		return nil, errT422ArchiveControl
	}
	file, path, info, volume, err := dispatchadmission.ProductionWorkspace()
	if err != nil {
		return nil, errT422ArchiveControl
	}
	custody, err := openT422ArchiveCustody(ctx, file, path, info, volume, *launch.request.Archive)
	if err != nil {
		return nil, errT422ArchiveControl
	}
	return &t422ArchiveControl{ctx: ctx, launch: launch, input: *launch.request.Archive, custody: custody}, nil
}

func (control *t422ArchiveControl) current(ctx context.Context) bool {
	current, err := dispatchadmission.ProductionSemanticState()
	return ctx != nil && ctx.Err() == nil && control.ctx.Err() == nil && err == nil &&
		current.ProducerID == 6 && current.Phase == 12 && control.launch.requestCurrent(ctx)
}

func (control *t422ArchiveControl) stop(cause error) error {
	control.mu.Lock()
	first := control.err == nil
	if first {
		control.err = errors.Join(errT422ArchiveControl, cause)
	}
	err := control.err
	control.mu.Unlock()
	if first {
		control.launch.fail(err)
	}
	return err
}

func (control *t422ArchiveControl) read(ctx context.Context) ([]byte, func(error), error) {
	if !control.current(ctx) {
		return nil, nil, control.stop(errT422ArchiveControl)
	}
	control.mu.Lock()
	if control.err != nil || control.closed || control.reading {
		control.mu.Unlock()
		return nil, nil, control.stop(errT422ArchiveControl)
	}
	control.reading = true
	value, err := control.custody.read(ctx, control.input)
	control.mu.Unlock()
	if err != nil || !control.current(ctx) {
		return nil, nil, control.stop(err)
	}
	body, err := json.Marshal(value)
	if err != nil || len(body)+1 > t422ArchiveTransitionBytes {
		return nil, nil, control.stop(err)
	}
	return append(body, '\n'), func(cause error) {
		if cause != nil || !control.current(ctx) {
			_ = control.stop(cause)
			return
		}
		control.mu.Lock()
		valid := control.err == nil && !control.closed && !control.reported && control.custody.check(ctx) == nil
		if valid {
			control.reported = true
		}
		control.mu.Unlock()
		if !valid {
			_ = control.stop(errT422ArchiveControl)
		}
	}, nil
}

func (control *t422ArchiveControl) close() error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.closed {
		return control.err
	}
	control.closed = true
	return errors.Join(control.err, control.custody.close())
}

func (state *t421ExactReadAccountingState) archiveRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.archive == nil || state.semantic == nil || state.archive.launch != state.semantic || request == nil || request.URL == nil ||
		request.URL.Path != t422ArchiveTransitionPath || request.URL.EscapedPath() != t422ArchiveTransitionPath ||
		request.Method != http.MethodGet || request.URL.RawQuery != "" || request.URL.ForceQuery || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return nil
	}
	return state.archive.read
}
