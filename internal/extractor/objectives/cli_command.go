package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objCLICommand = Objective{
	ID:                "exposure.cli_command",
	Kind:              model.KindExposure,
	Type:              "cli_command",
	Description:       "CLI command entrypoints, script command handlers, and Lambda function handlers",
	ConnectionContext: "Map command-triggered execution paths to dependencies.",
}
