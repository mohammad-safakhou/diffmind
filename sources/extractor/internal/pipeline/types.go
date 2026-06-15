package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
)

// Shared DTOs and client interfaces live in extraction and llmrun so stages do
// not depend on the orchestrator. These aliases keep pipeline code concise.
type (
	openCodeAPI     = llmrun.Client
	pauseHandler    = llmrun.PauseHandler
	verbosePrompter = llmrun.VerbosePrompter
	tokenReader     = llmrun.TokenReader
	sessionState    = llmrun.SessionState
	watchdog        = llmrun.Watchdog
	PathMapper      = extraction.PathMapper
	objectiveHints  = extraction.ObjectiveHints

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
)

type (
	Result            = extraction.Result
	Failure           = extraction.Failure
	IntermediateState = extraction.IntermediateState
)
