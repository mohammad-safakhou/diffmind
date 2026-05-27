package agents

// index_stage.go — the legacy SCIP-based index stage has been replaced by
// ast_stage.go which runs the tree-sitter AST index engine.
//
// This file is intentionally empty. All indexing logic lives in ast_stage.go.
// The runASTIndexStage method is called from pipeline.go in the position
// previously occupied by runIndexStage.
