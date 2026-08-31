package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objWorkflowOrchestration = Objective{
	ID:                "dependency.workflow_orchestration",
	Kind:              model.KindDependency,
	Type:              "workflow_orchestration",
	Description:       "Workflow/orchestrator interactions such as Camunda external task clients and process variables",
	ConnectionContext: "Connection mapping must keep orchestration separate from direct service calls.",
}
