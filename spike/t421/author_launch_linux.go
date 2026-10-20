//go:build linux

package t421

import (
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type linuxAuthorMode uint8

const (
	linuxAuthorUnbound linuxAuthorMode = iota
	linuxAuthorStandalone
	linuxAuthorShared
)

// Constructed only from the current genuine controller view, never supplied
// as public command/input authority. Parent nil selects the fixed standalone recipe.
type linuxAuthorLaunch struct {
	trace      *linuxEpochTraceOwner // Only genuine complete held epoch scope sets this.
	controller *dispatchadmission.Controller
	parent     *dispatchadmission.LocalProducer
	producer   uint32
	view       dispatchadmission.ProducerLaunch
	control    dispatchadmission.PhaseControlConfig
}

func (launch linuxAuthorLaunch) validate(index int, phase uint32) error {
	if index < 0 || index >= 3 || !launch.parent.OnController(launch.controller) || launch.producer != uint32(7+index) || phase != uint32(2+2*index) || launch.view.Producer.ID != launch.producer || launch.view.Phase != phase || !slices.Equal(launch.view.Producer.Sites, dispatchadmission.AuthorSites()) {
		return ErrExecutionAuthorCustody
	}
	count, err := launch.parent.Count()
	if err != nil || count.Producer != executionRootProducer || !count.Attached || count.Closed {
		return ErrExecutionAuthorCustody
	}
	return nil
}

// owner.mu serializes mode selection and future launches. Controller views are
// refreshed before Start; after attachment DA's exact phase check remains authoritative.
func (owner *LinuxExecutionAuthorCustody) bindLaunchLocked(index int, launch *linuxAuthorLaunch) error {
	mode := linuxAuthorStandalone
	if launch.parent != nil {
		mode = linuxAuthorShared
		if !launch.parent.OnController(launch.controller) {
			return ErrExecutionAuthorCustody
		}
		view, err := launch.controller.ProducerLaunch(launch.producer)
		if err != nil {
			return ErrExecutionAuthorCustody
		}
		launch.view = view
		launch.control = dispatchadmission.PhaseControlConfig{TerminalAuthor: true, Phases: []uint32{view.Phase}, InitialPhase: view.Phase, MaximumPhases: 1, MaximumWireBytes: 4 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
		if launch.validate(index, owner.projection.phases[index]) != nil {
			return ErrExecutionAuthorCustody
		}
	} else {
		launch.producer = 1
	}
	if owner.mode != linuxAuthorUnbound && (owner.mode != mode || mode == linuxAuthorShared && (owner.controller != launch.controller || owner.parent != launch.parent)) {
		return ErrExecutionAuthorCustody
	}
	owner.mode = mode
	owner.controller, owner.parent = launch.controller, launch.parent
	return nil
}
