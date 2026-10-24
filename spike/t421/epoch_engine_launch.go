package t421

import "github.com/bmeddeb/phebs/internal/dispatchadmission"

func epochEngineRuntimeRecipe(recipe string) string {
	switch recipe {
	case "":
		return dispatchadmission.ProductionLinuxForegroundGitV2
	case dispatchadmission.EngineMeasurementCheckpointV2:
		return dispatchadmission.ProductionLinuxForegroundGitV3
	default:
		return ""
	}
}

// The same value owns the wire debit, endpoint and bootstrap identity. This
// prepares EM02 plumbing; the native constructor still selects legacy EM01.
func epochEngineLaunchBinding(recipe string, maximum uint32, run *ExecutionEpochOneRun, producer dispatchadmission.Producer, input [32]byte, tools []dispatchadmission.ProductionToolBinding) (dispatchadmission.EngineMeasurementBinding, error) {
	var zero dispatchadmission.EngineMeasurementBinding
	if run == nil || run.epoch.Epoch < 1 || run.epoch.Epoch > 4 || producer.ID != uint32(run.epoch.Epoch+1) ||
		!sameExecutionToolBindings(tools, tools, recipe) {
		return zero, ErrExecutionEpochOne
	}
	if recipe == dispatchadmission.EngineMeasurementCheckpointV2 {
		checkpointShape := run.epoch.Epoch == 3 && run.checkpointAllowed || run.epoch.Epoch == 4 && run.checkpointRecovery != nil && run.checkpointPrior != nil
		if run.backupAllowed || run.archiveInput != nil || !checkpointShape {
			return zero, ErrExecutionEpochOne
		}
	}
	binding := dispatchadmission.EngineMeasurementBinding{Recipe: recipe, ProducerID: producer.ID, ProducerBinding: producer.Binding, InputSHA256: input}
	if _, err := binding.WireBytes(maximum); err != nil {
		return zero, ErrExecutionEpochOne
	}
	return binding, nil
}

func reserveEpochEngineWire(output *checkoutCommandOutput, binding dispatchadmission.EngineMeasurementBinding, maximum uint32) error {
	wire, err := binding.WireBytes(maximum)
	if err != nil || output == nil || output.remaining < int64(wire) {
		return ErrExecutionEpochOne
	}
	output.remaining -= int64(wire)
	return nil
}
