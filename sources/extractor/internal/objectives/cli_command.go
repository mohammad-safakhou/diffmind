package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objCLICommand = Objective{
	ID:          "exposure.cli_command",
	Kind:        model.KindExposure,
	Type:        "cli_command",
	Description: "CLI command entrypoints, script command handlers, and Lambda function handlers",
	DiscoveryPrompt: `Find CLI command entrypoints, main method dispatch, management commands, and AWS Lambda function handlers that trigger business flows.

PATTERNS TO CHECK:
- Java: main() methods, Spring Shell @ShellComponent, Picocli @Command
- Python: console_scripts in setup.py/pyproject.toml, argparse, click @command, Lambda handler functions
- Node.js: bin scripts in package.json, commander/yargs commands
- Go: cobra commands, flag-based dispatch
- AWS Lambda: handler functions (def handler, exports.handler, @LambdaHandler)
- AWS SAM: template.yaml Handler definitions

FOR EACH ENTRYPOINT EXTRACT:
- Command name/path
- Arguments and options
- Handler function/class
- What it triggers

BOUNDARY (do not double-report): a CommandLineRunner / batch job that is gated
by a Spring @Profile (or @ConditionalOnProperty) and triggered externally (e.g.
a Kubernetes CronJob launching the app with a profile) is a SCHEDULED_JOB, not a
cli_command. Only report a genuine interactive/operator command or a true
process entrypoint (the application's main launcher) here.

If this is a standard web service with no CLI commands or Lambda handlers, return {"items": []}.`,
	DetailPrompt:      "For this CLI entrypoint, extract arguments, command options, validation, and ordered downstream operations.",
	ConnectionContext: "Map command-triggered execution paths to dependencies.",
}
