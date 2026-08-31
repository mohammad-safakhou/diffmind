package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objCommandExec = Objective{
	ID:                "dependency.command_exec",
	Kind:              model.KindDependency,
	Type:              "command_exec",
	Description:       "External command/process execution",
	ConnectionContext: "Connection mapping must include executed command and trigger condition.",
}
