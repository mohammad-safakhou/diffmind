package core

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
)

// Deprecated compatibility aliases. New packages import extraction or llmrun
// directly; these disappear when the agents package is removed.
type (
	OpenCodeAPI     = llmrun.Client
	PauseHandler    = llmrun.PauseHandler
	VerbosePrompter = llmrun.VerbosePrompter
	TokenReader     = llmrun.TokenReader
	SessionState    = llmrun.SessionState

	PendingPermission = llmrun.PendingPermission
	PendingQuestion   = llmrun.PendingQuestion

	LLMLocation = extraction.Location
	LLMEvidence = extraction.Evidence
	LLMInput    = extraction.Input
	LLMEntity   = extraction.Candidate

	RepoFacts       = extraction.RepoFacts
	LangFact        = extraction.LanguageFact
	DiscoveryResult = extraction.DiscoveryResult
	DetailJob       = extraction.DetailJob
	DetailResult    = extraction.DetailResult
)
