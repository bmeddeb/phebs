package recovery

import (
	"errors"
	"math"
)

// BackupCheckpointMaximum bounds attempts, including failed samples and
// cleanup. Success has stage + export + five archive terminals + manifest +
// publication (9), plus observation's four and relationship's two nested
// verification checkpoints (15). Failure can replace the final publication
// checkpoint with the stage's two cleanup checkpoints (16). A late nested
// relationship verification failure reaches the same bound, not a larger one.
func BackupCheckpointMaximum() uint32 { return 16 }

// RestoreCheckpointMaximum derives a conservative operation-attempt bound from
// the existing phase transaction allowance, not a new work allowance. Verify
// contributes 14, five installations 10, import/repair scopes three each, and
// replay directory creation/removal plus its terminal spool attempt four: 34.
// Each replay unit contributes a pre-unlink and a settled-submit checkpoint.
// Two admitted metadata transactions precede any unit. A spooled unit whose
// admission fails consumes the terminal pair instead of EOF and stops before
// later installations and repair. Earlier helper failures do not exceed their
// successful scope count. No store success, phase coverage, or sample success
// is implied by this upper bound.
func RestoreCheckpointMaximum(phaseTransactions uint64) (uint32, error) {
	const fixed = uint64(14 + 5*2 + 3 + 3 + 4)
	const bootstrapTransactions = uint64(2)
	if phaseTransactions < bootstrapTransactions ||
		phaseTransactions-bootstrapTransactions > (math.MaxUint32-fixed)/2 {
		return 0, errors.New("restore checkpoint transaction bound is invalid")
	}
	return uint32(fixed + 2*(phaseTransactions-bootstrapTransactions)), nil
}
