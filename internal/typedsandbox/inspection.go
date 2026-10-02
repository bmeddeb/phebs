package typedsandbox

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
)

// RecordedInspection is a read-only observation, not authority to signal a
// process or mutate custody. The caller must retain its own attempt pin and
// recheck process identity before acting. No control/source file is required.
type RecordedInspection struct {
	ContainerID, Name, Inputs, Controls string
	PID                                 int
	Control                             ControlIdentity
	Allowance                           Allowance
	Scratch                             ScratchAuthority
}

// ValidateRecordedMetadata authenticates an optional retained journal without
// contacting the daemon or authorizing mutation. The caller must first census
// the pinned owner namespace; a pending journal without its main is refused.
func ValidateRecordedMetadata(ctx context.Context, options RecoveryOptions, requestDigest string) error {
	if ctx == nil || !validOptionPath(options.Inputs) || !hostDigest(options.PlanningDigest) || !hostDigest(options.AttemptDigest) {
		return ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := journalPath(Options{Inputs: options.Inputs})
	if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
		if _, pending := os.Lstat(name + ".next"); errors.Is(pending, os.ErrNotExist) {
			return ctx.Err()
		}
		return ErrCustody
	} else if err != nil {
		return ErrCustody
	}
	if !validOptionPath(options.Socket) || !imageID(options.ImageID) || !hostDigest(requestDigest) {
		return ErrCustody
	}
	owner, err := readJournalMetadata(name)
	if err != nil || owner.Socket != options.Socket || owner.ImageID != options.ImageID || owner.Inputs != options.Inputs || owner.Control.PlanningDigest != options.PlanningDigest || owner.Control.AttemptDigest != options.AttemptDigest {
		return ErrCustody
	}
	name, err = HostScratchRootName(options.PlanningDigest, options.AttemptDigest)
	if err != nil || owner.Control.RequestDigest != requestDigest || owner.Scratch == nil || owner.Scratch.Source != HostScratchBase+"/"+name+"/scratch" {
		return ErrCustody
	}
	if _, err = cleanupOwner(owner.options(), owner); err != nil {
		return err
	}
	return ctx.Err()
}

func InspectRecorded(ctx context.Context, options RecoveryOptions) (RecordedInspection, error) {
	if runtime.GOOS != "linux" {
		return RecordedInspection{}, ErrRefused
	}
	return inspectRecorded(ctx, options)
}

func inspectRecorded(ctx context.Context, options RecoveryOptions) (RecordedInspection, error) {
	if ctx == nil || !validOptionPath(options.Socket) || !validOptionPath(options.Inputs) || !imageID(options.ImageID) || !hostDigest(options.PlanningDigest) || !hostDigest(options.AttemptDigest) {
		return RecordedInspection{}, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return RecordedInspection{}, err
	}
	original, err := readJournalMetadata(journalPath(Options{Inputs: options.Inputs}))
	if err != nil || original.Socket != options.Socket || original.ImageID != options.ImageID || original.Inputs != options.Inputs || original.Control.PlanningDigest != options.PlanningDigest || original.Control.AttemptDigest != options.AttemptDigest {
		return RecordedInspection{}, ErrCustody
	}
	owner, err := cleanupOwner(original.options(), original)
	if err != nil || !containerID(owner.ContainerID) || owner.Scratch == nil {
		return RecordedInspection{}, ErrCustody
	}
	c, err := newClient(owner.options())
	if err != nil {
		return RecordedInspection{}, err
	}
	defer c.http.CloseIdleConnections()
	checkDaemon := func() error {
		var info struct{ ID string }
		if _, e := c.request(ctx, "GET", "/info", nil, &info, 200); e != nil || info.ID != owner.DaemonID {
			return ErrCustody
		}
		return nil
	}
	if err = checkDaemon(); err != nil {
		return RecordedInspection{}, err
	}
	got, status, err := c.inspect(ctx, owner.ContainerID)
	if err != nil || status != 200 || verify(got, owner.options(), owner) != nil || !got.State.Running || got.State.Paused || got.State.Restarting || got.State.Dead || got.State.OOMKilled || got.State.Pid <= 1 {
		return RecordedInspection{}, ErrCustody
	}
	if err = checkDaemon(); err != nil {
		return RecordedInspection{}, err
	}
	main, err := readJournalMetadata(journalPath(owner.options()))
	if err != nil || !reflect.DeepEqual(main, original) {
		return RecordedInspection{}, ErrCustody
	}
	after, err := cleanupOwner(main.options(), main)
	if err != nil || !reflect.DeepEqual(after, owner) {
		return RecordedInspection{}, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return RecordedInspection{}, err
	}
	return RecordedInspection{ContainerID: got.ID, Name: owner.Name, Inputs: owner.Inputs, Controls: owner.Controls, PID: got.State.Pid, Control: owner.Control, Allowance: owner.Allowance, Scratch: *owner.Scratch}, nil
}
