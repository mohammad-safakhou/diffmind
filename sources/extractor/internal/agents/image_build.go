package agents

// image_build.go — the Docker-based SCIP indexer image pipeline has been
// retired in favour of the tree-sitter AST engine (ast_stage.go).
//
// This file is intentionally empty. The orchestrator struct no longer
// contains buildDone, buildResult, or indexerOverride fields; the
// kickoffImageBuild and waitForImageBuild methods are gone.
