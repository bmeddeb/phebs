package typedsandbox

import (
	"context"
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
