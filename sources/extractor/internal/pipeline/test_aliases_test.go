package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	detailstage "github.com/mohammad-safakhou/diffmind/internal/stage/detail"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	reexaminestage "github.com/mohammad-safakhou/diffmind/internal/stage/reexamine"
	"github.com/mohammad-safakhou/diffmind/internal/stage/repofacts"
)

// Test-only aliases preserve legacy characterization tests while production
// pipeline code calls the owning packages directly.

const detailBatchHardCap = detailstage.DetailBatchHardCap
const maxSymbolHints = discoverystage.MaxSymbolHints
const discoveryShardHardCap = discoverystage.DiscoveryShardHardCap

var (
	ClassifyError           = llmrun.ClassifyError
	ExtractHTTPStatus       = llmrun.ExtractHTTPStatus
	ShouldReportHTTPStatus  = llmrun.ShouldReportHTTPStatus
	BuildRepoFactsPrompt    = repofacts.BuildPrompt
	BuildDiscoveryPrompt    = extraction.BuildDiscoveryPrompt
	BuildDetailPrompt       = extraction.BuildDetailPrompt
	BuildReexaminePrompt    = extraction.BuildReexaminePrompt
	ScopeFrameworkPatterns  = extraction.ScopeFrameworkPatterns
	DetectedLanguageSet     = extraction.DetectedLanguageSet
	ConfirmedDiscoveryBlock = extraction.ConfirmedDiscoveryBlock
	Itoa                    = extraction.Itoa
	ToBase                  = extraction.ToBase
	ForceObjectiveType      = extraction.ForceObjectiveType
	EnrichEntityGrouping    = extraction.EnrichEntityGrouping
	RepoFactsBlock          = extraction.RepoFactsBlock
	MonorepoScopeLine       = extraction.MonorepoScopeLine
	AstHintsBlock           = extraction.AstHintsBlock
	DiscoveryScopeBlock     = extraction.DiscoveryScopeBlock
	ExampleBlock            = extraction.ExampleBlock
	DetailKeysLine          = extraction.DetailKeysLine

	DetailGroups       = detailstage.DetailGroups
	mergeEnrichment    = detailstage.MergeEnrichment
	pinIdentityDetails = detailstage.PinIdentityDetails
	identityDetailKeys = detailstage.IdentityDetailKeys
	NameTokens         = detailstage.NameTokens

	ResolveResourceName        = discoverystage.ResolveResourceName
	IsPlaceholder              = discoverystage.IsPlaceholder
	SplitPlaceholder           = discoverystage.SplitPlaceholder
	ResolvePlaceholder         = discoverystage.ResolvePlaceholder
	ConfigValue                = discoverystage.ConfigValue
	TrailingResourceSegment    = discoverystage.TrailingResourceSegment
	KeySegmentName             = discoverystage.KeySegmentName
	buildObjectiveHints        = discoverystage.BuildObjectiveHints
	planDiscoveryShards        = discoverystage.PlanShards
	mergeShardEntities         = discoverystage.MergeShardEntities
	distinctDirs               = discoverystage.DistinctDirs
	deterministicCommandExec   = discoverystage.DeterministicCommandExec
	deterministicQueuePublish  = discoverystage.DeterministicQueuePublish
	deterministicOutboundRPC   = discoverystage.DeterministicOutboundRPC
	deterministicStreamConsume = discoverystage.DeterministicStreamConsume
	deterministicDBOperations  = discoverystage.DeterministicDBOperations
	inferConfigDBPlatform      = discoverystage.InferConfigDBPlatform
	stampInferredDBPlatform    = discoverystage.StampInferredDBPlatform
	entityFromFrameworkBinding = discoverystage.EntityFromFrameworkBinding
	mergeDiscoveryResults      = discoverystage.MergeDiscoveryResults
	matchGRPCStubCall          = discoverystage.MatchGRPCStubCall
	matchCommandExec           = discoverystage.MatchCommandExec
	matchQueuePublish          = discoverystage.MatchQueuePublish

	shouldReexamine              = extraction.ShouldReexamine
	missingRequiredDetails       = extraction.MissingRequiredDetails
	discoverySemanticKey         = extraction.DiscoverySemanticKey
	isCompleteDeterministicSeed  = extraction.IsCompleteDeterministicSeed
	hasDeterministicEvidence     = extraction.HasDeterministicEvidence
	splitMethodPath              = extraction.SplitMethodPath
	splitServiceMethod           = extraction.SplitServiceMethod
	hasDetailKey                 = extraction.HasDetailKey
	seedStructurallyUnverifiable = reexaminestage.SeedStructurallyUnverifiable
	downgradeConfidence          = reexaminestage.DowngradeConfidence
	appendUniqueTag              = reexaminestage.AppendUniqueTag

	newWatchdog      = llmrun.NewWatchdog
	runLiveness      = llmrun.RunLiveness
	decidePermission = llmrun.DecidePermission
)

type probeSnapshot = llmrun.ProbeSnapshot
type livenessConfig = llmrun.LivenessConfig
type livenessReport = llmrun.LivenessReport
type livenessProbe = llmrun.LivenessProbe
type aborter = llmrun.Aborter
type symbolHint = extraction.SymbolHint
type bindingHint = extraction.BindingHint
