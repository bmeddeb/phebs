package t4013

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	custodyControlSchema = "t4013-custody-supervision-v1"

	custodyOperationPrepare custodyOperation = "prepare"
	custodyOperationExecute custodyOperation = "execute"

	custodyPhaseCreated    custodyPhase = "created"
	custodyPhaseLive       custodyPhase = "live"
	custodyPhaseDrained    custodyPhase = "drained"
	custodyPhaseFinalizing custodyPhase = "finalizing"
	custodyPhaseTerminal   custodyPhase = "terminal"

	custodyStatusLive          custodyStatus = "live"
	custodyStatusCreated       custodyStatus = "created"
	custodyStatusDrained       custodyStatus = "drained"
	custodyStatusTerminal      custodyStatus = "terminal"
	custodyStatusIndeterminate custodyStatus = "indeterminate"
)

var errCustodyDescendantsLive = errors.New("T40.13 custody descendants remain live")

type custodyOperation string
type custodyPhase string
type custodyStatus string

type custodyControlState struct {
	Schema           string           `json:"schema"`
	Token            string           `json:"token"`
	PlanDigest       string           `json:"plan_digest"`
	Workspace        string           `json:"workspace"`
	Operation        custodyOperation `json:"operation"`
	Phase            custodyPhase     `json:"phase"`
	CheckpointDigest string           `json:"checkpoint_digest"`
}

// custodySupervision owns the controller lock and the one lease descriptor
// inherited by every child launched while the control is live or finalizing.
type custodySupervision struct {
	directory  string
	state      custodyControlState
	controller *os.File
	lease      *os.File
}

// Token returns the durable random identity bound to this supervision control.
func (supervision *custodySupervision) Token() string {
	if supervision == nil {
		return ""
	}
	return supervision.state.Token
}

func newCustodyToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("create T40.13 custody token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func custodyControlDirectory(workspace string) (string, string, error) {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) == string(filepath.Separator) {
		return "", "", errors.New("T40.13 custody workspace must be absolute and non-root")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(workspace)))
	if err != nil {
		return "", "", fmt.Errorf("resolve T40.13 custody parent: %w", err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.Join(err, errors.New("T40.13 custody parent is invalid"))
	}
	canonical := filepath.Join(parent, filepath.Base(filepath.Clean(workspace)))
	return canonical, canonical + ".t4013-supervision", nil
}

func validateCustodyState(state custodyControlState) error {
	workspace, _, err := custodyControlDirectory(state.Workspace)
	if err != nil || workspace != state.Workspace || state.Schema != custodyControlSchema ||
		!hexIdentity(state.Token, 64) || !digestIdentity(state.PlanDigest) {
		return errors.Join(err, errors.New("T40.13 custody control identity is invalid"))
	}
	if state.Operation != custodyOperationPrepare && state.Operation != custodyOperationExecute {
		return errors.New("T40.13 custody operation is invalid")
	}
	switch state.Phase {
	case custodyPhaseCreated, custodyPhaseLive:
		if state.CheckpointDigest != "" {
			return errors.New("T40.13 active custody unexpectedly has a checkpoint digest")
		}
	case custodyPhaseDrained, custodyPhaseFinalizing, custodyPhaseTerminal:
		if state.Operation == custodyOperationPrepare && state.CheckpointDigest != "" {
			return errors.New("T40.13 prepare custody unexpectedly has a checkpoint digest")
		}
		if state.Operation == custodyOperationExecute && !digestIdentity(state.CheckpointDigest) {
			return errors.New("T40.13 execute custody lacks its checkpoint digest")
		}
	default:
		return errors.New("T40.13 custody phase is invalid")
	}
	return nil
}

func confirmCustodySupervisionRetired(
	workspace, planDigest, token string,
	operation custodyOperation,
	checkpointDigest string,
) error {
	workspace, directory, err := custodyControlDirectory(workspace)
	if err != nil {
		return err
	}
	expected := custodyControlState{
		Schema: custodyControlSchema, Token: token, PlanDigest: planDigest,
		Workspace: workspace, Operation: operation, Phase: custodyPhaseTerminal,
		CheckpointDigest: checkpointDigest,
	}
	if err := validateCustodyState(expected); err != nil {
		return err
	}
	if err := confirmCustodyRetirement(directory, expected); err != nil {
		return fmt.Errorf("finish T40.13 custody supervision retirement: %w", err)
	}
	if err := confirmCustodyDeletionDurable(directory); err != nil {
		return fmt.Errorf("T40.13 custody supervision is not durably retired: %w", err)
	}
	return nil
}
