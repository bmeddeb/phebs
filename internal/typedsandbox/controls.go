package typedsandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
)

const (
	ControlSealFile     = "control-seal.json"
	MaxControlSealBytes = 4096
	ControlPlan         = "plan"
	ControlExecute      = "execute"
)

// ControlIdentity comes from the trusted controller's verified immutable
// snapshot. It is not readiness proof by itself. The caller keeps that snapshot
// and its attempt pin open for the entire Run, including joined cleanup.
type ControlIdentity struct {
	PlanningDigest string `json:"planning_digest"`
	AttemptDigest  string `json:"attempt_digest"`
	RequestDigest  string `json:"request_digest"`
	Phase          string `json:"phase"`
	SealDigest     string `json:"seal_digest"`
	Device         uint64 `json:"device"`
	Inode          uint64 `json:"inode"`
}

func (c ControlIdentity) Validate() error {
	if !hostDigest(c.PlanningDigest) || !hostDigest(c.AttemptDigest) || !hostDigest(c.RequestDigest) || !hostDigest(c.SealDigest) || c.Device == 0 || c.Inode == 0 {
		return ErrRefused
	}
	if c.Phase == ControlPlan && c.RequestDigest == c.PlanningDigest || c.Phase == ControlExecute && c.RequestDigest != c.PlanningDigest {
		return nil
	}
	return ErrRefused
}

func controlDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Sandbox verification deliberately checks only the trusted scalar reference,
// fixed seal hash, and exact scratch authority. The workspace owner separately
// verifies every canonical snapshot member and retains the shared attempt pin.
// Holding this FD does not by itself prevent namespace mutation or unmounting.
func openControls(ctx context.Context, options Options) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := openControlDirectory(options.Controls)
	if err != nil {
		return nil, ErrRefused
	}
	if err = verifyControls(ctx, options, root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func verifyControlPath(options Options) bool {
	return options.Control.Validate() == nil && options.Controls == filepath.Join(filepath.Dir(options.Inputs), "controls-"+options.Control.Phase)
}

// RecoveryOptions is authenticated retained-attempt identity, not current
// execution admission. Inputs must be the exact path from that trusted owner's
// manifest, even when the directory no longer exists. Phase, scratch and seal
// identity come only from the matching durable journal, never current intent.
type RecoveryOptions struct {
	Socket, ImageID, Inputs       string
	PlanningDigest, AttemptDigest string
}

func RecoverRecorded(ctx context.Context, options RecoveryOptions) (Result, error) {
	if runtime.GOOS != "linux" {
		return Result{}, ErrRefused
	}
	return recoverRecorded(ctx, options)
}

func recoverRecorded(ctx context.Context, options RecoveryOptions) (Result, error) {
	if ctx == nil || !validOptionPath(options.Socket) || !validOptionPath(options.Inputs) || !imageID(options.ImageID) || !hostDigest(options.PlanningDigest) || !hostDigest(options.AttemptDigest) {
		return Result{}, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	owner, err := readJournalMetadata(journalPath(Options{Inputs: options.Inputs}))
	if err != nil || owner.Socket != options.Socket || owner.ImageID != options.ImageID || owner.Inputs != options.Inputs || owner.Control.PlanningDigest != options.PlanningDigest || owner.Control.AttemptDigest != options.AttemptDigest {
		return Result{}, ErrCustody
	}
	// recoverContainer and cleanupOwner recheck main+pending before the first
	// daemon request, and repeat that exact ownership proof before removal.
	return recoverSelected(ctx, owner.options(), owner)
}
