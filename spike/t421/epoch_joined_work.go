package t421

// Fixed slots are producer 2..6, then 10 and 11. Each record belongs to one
// actual root Start/finish, not an epoch result that may carry archive copies.
// All payloads are values; neither raw output nor custody pointers are retained.
type executionJoinedWorkRecord struct {
	Producer             uint32
	Input                [32]byte
	Joined, SessionEmpty bool
	Attempts             ExecutionAttemptObservation
	IndexOffers          ExecutionIndexObservation // Servers only; archives have no index-offer profile.
}

type executionJoinedWork struct {
	Records [7]executionJoinedWorkRecord
	Err     error // A replay/invalid owner never erases an earlier positive prefix.
}

func executionJoinedWorkSlot(producer uint32) int {
	switch {
	case producer >= 2 && producer <= 6:
		return int(producer - 2)
	case producer == 10 || producer == 11:
		return int(producer - 5)
	default:
		return -1
	}
}

// Called outside run.mu. Existing finish/one-shot archive paths own the true
// joins; this private setter neither authenticates supplied data nor reparses it.
func (flow *ExecutionEpochOne) retainJoinedWork(record executionJoinedWorkRecord) error {
	if flow == nil {
		return errExecutionAttempts
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	slot := executionJoinedWorkSlot(record.Producer)
	if slot < 0 || record.Input == ([32]byte{}) || flow.joinedWork.Records[slot].Producer != 0 {
		flow.joinedWork.Err = errExecutionAttempts
		return errExecutionAttempts
	}
	flow.joinedWork.Records[slot] = record
	return nil
}

// The actual phase-fifteen final defer copies this beside its existing current
// DA/SA snapshot pair, before shared lifetime cancellation. It also retains
// missing/failed slots on a consumed teardown failure; no zero work is inferred.
func (flow *ExecutionEpochOne) joinedWorkSnapshot() executionJoinedWork {
	if flow == nil {
		return executionJoinedWork{Err: errExecutionAttempts}
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.joinedWork
}

// This is seven producer-local subsets, not a whole-phase verdict. In
// particular phase eight keeps predecessor 4 and successor 5 independent;
// no addition, max folding, overflow arithmetic or receipt defaults occur here.
func (work executionJoinedWork) complete() bool {
	if work.Err != nil {
		return false
	}
	for slot, record := range work.Records {
		if executionJoinedWorkSlot(record.Producer) != slot || record.Input == ([32]byte{}) ||
			!record.Joined || !record.SessionEmpty || !record.Attempts.Complete ||
			record.Producer <= 6 && (!record.IndexOffers.Bound || !record.IndexOffers.Complete) {
			return false
		}
	}
	return true
}
