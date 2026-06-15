package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objCommandExec = Objective{
	ID:                "dependency.command_exec",
	Kind:              model.KindDependency,
	Type:              "command_exec",
	Description:       "External command/process execution",
	DiscoveryPrompt:   "Find shell/process execution dependencies (Runtime.exec, ProcessBuilder, os command wrappers, subprocess, scripts). If none exist, return {\"items\": []}.",
	ConnectionContext: "Connection mapping must include executed command and trigger condition.",
}
