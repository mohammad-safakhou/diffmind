package agents

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
)

// The shared DTOs and client interfaces live in internal/agents/core so every
// stage package can depend on them without depending on the orchestrator.
// These aliases keep the orchestrator's existing (lower-case) names working;
// stage packages reference the core.* names directly.
type (
	openCodeAPI     = llmrun.Client
	pauseHandler    = llmrun.PauseHandler
	verbosePrompter = llmrun.VerbosePrompter
	tokenReader     = llmrun.TokenReader
	sessionState    = llmrun.SessionState

	PendingPermission = llmrun.PendingPermission
	PendingQuestion   = llmrun.PendingQuestion

	llmLocation = extraction.Location
	llmEvidence = extraction.Evidence
	llmInput    = extraction.Input
	llmEntity   = extraction.Candidate

	repoFacts       = extraction.RepoFacts
	langFact        = extraction.LanguageFact
	discoveryResult = extraction.DiscoveryResult
	detailJob       = extraction.DetailJob
	detailResult    = extraction.DetailResult
)

type (
	Result            = extraction.Result
	Failure           = extraction.Failure
	IntermediateState = extraction.IntermediateState
)
