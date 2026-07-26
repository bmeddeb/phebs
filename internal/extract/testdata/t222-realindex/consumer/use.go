package consumer

import "example.invalid/t222-realindex/generated"

func Read(item *shared.Item) string {
	return item.GetWorkflowId()
}

func Clear(item *shared.Item) {
	item.WorkflowId = nil
}
