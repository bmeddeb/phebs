package provider

import (
	"errors"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/sys/unix"
)

type PlanningScratch struct {
	Available  bool   `json:"available"`
	FreeBlocks uint64 `json:"free_blocks"`
	FreeInodes uint64 `json:"free_inodes"`
}
type PlanningCommandError struct {
	Cause   error
	Scratch PlanningScratch
}

func (e *PlanningCommandError) Error() string { return "planning command failed" }
func (e *PlanningCommandError) Unwrap() error { return e.Cause }
func planningFailure(e error) error           { return planningFailureWith(e, unix.Statfs) }
func planningFailureWith(e error, statfs func(string, *unix.Statfs_t) error) error {
	out := &PlanningCommandError{Cause: e}
	var s unix.Statfs_t
	if statfs("/scratch", &s) == nil {
		out.Scratch = PlanningScratch{Available: true, FreeBlocks: s.Bfree, FreeInodes: s.Ffree}
	}
	return out
}

type Failure struct {
	Stage                   string             `json:"stage"`
	Reason                  typedindex.Refusal `json:"reason"`
	PlanningScratch         *PlanningScratch   `json:"planning_scratch,omitempty"`
	PlanningProcess         *ProcessDiagnostic `json:"planning_process,omitempty"`
	FinalProcess            *ProcessDiagnostic `json:"final_process,omitempty"`
	FinalQuiescenceFailed   bool               `json:"final_quiescence_failed"`
	FinalVerificationFailed bool               `json:"final_verification_failed"`
}

func reason(err error) typedindex.Refusal {
	var r typedindex.Refusal
	if errors.As(classify(err), &r) {
		return r
	}
	return typedindex.ExecutionFailed
}
