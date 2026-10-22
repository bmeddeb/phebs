package t421

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// These closed adapters carry native authority separately from the coordinator's
// logical state. Historical constructors leave them nil.
type executionAuthorNativeAdapter interface {
	check(context.Context) error
	checkSource(context.Context, *ExecutionCorpusAuthorResponse) error
	next(context.Context, *dispatchadmission.Controller, *dispatchadmission.LocalProducer, uint32, *ExecutionEpochOneRun) (ExecutionAuthorResult, error)
}
type executionConfigNativeAdapter interface {
	check(context.Context, uint64) error
	release(context.Context, uint64) error
}
type executionEpochNativeAdapter interface {
	tools(context.Context, uint64) (string, []dispatchadmission.ProductionToolBinding, []string, error)
	start(context.Context, context.Context, *ExecutionEpochOneRun, uint32) (bool, error)
	startWait(context.Context, *ExecutionEpochOneRun, uint32) (<-chan error, error)
	finish(*ExecutionEpochOneRun, <-chan error, bool, error, time.Time, bool) (executionProcessDeath, error)
	workspace() (*os.File, string, [2]int32)
	engineMeasurements(uint64) uint32
	serveEngineMeasurement(context.Context, *ExecutionEpochOneRun, *net.UnixConn, dispatchadmission.EngineMeasurementBinding, uint32) <-chan error
}
