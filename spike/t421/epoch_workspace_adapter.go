package t421

import "os"

func (flow *ExecutionEpochOne) hasWorkspace() bool {
	return flow != nil && (flow.workspace != nil || flow.native != nil)
}
func (flow *ExecutionEpochOne) workspaceDescriptor() (*os.File, string, [2]int32) {
	if flow.native != nil {
		return flow.native.workspace()
	}
	if flow.workspace == nil {
		return nil, "", [2]int32{}
	}
	return flow.workspace.file, flow.workspace.path, flow.workspace.volume
}
