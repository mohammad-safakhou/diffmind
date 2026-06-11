package agents

import (
	"github.com/mohammad-safakhou/diffmind/internal/agents/core"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	detailstage "github.com/mohammad-safakhou/diffmind/internal/stage/detail"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
)

// transitional shims so the orchestrator keeps terse names; stage packages use
// core.* directly.

// types
type (
	PathMapper       = core.PathMapper
	StuckError       = llmrun.StuckError
	ProgressReporter = core.ProgressReporter
)

// sentinel
var ErrStuck = llmrun.ErrStuck

// grouping.go consts (referenced by agents tests)
const detailBatchHardCap = detailstage.DetailBatchHardCap

// convert.go
var (
	ToBase            = core.ToBase
	ToLocations       = core.ToLocations
	ToEvidence        = core.ToEvidence
	FillCondition     = core.FillCondition
	DefaultStr        = core.DefaultStr
	ErrString         = core.ErrString
	SafeJobID         = core.SafeJobID
	DedupeUnresolved  = core.DedupeUnresolved
	DedupeStrings     = core.DedupeStrings
	ParseEntities     = core.ParseEntities
	ParseSingleEntity = core.ParseSingleEntity
	ParseRepoFacts    = core.ParseRepoFacts
)

// classify.go
var (
	CanonicalObjectiveType       = core.CanonicalObjectiveType
	NormalizeType                = core.NormalizeType
	ForceObjectiveType           = core.ForceObjectiveType
	EntitySchemaForObjective     = core.EntitySchemaForObjective
	EntityListSchemaForObjective = core.EntityListSchemaForObjective
	EnrichEntityGrouping         = core.EnrichEntityGrouping
	DeriveGrouping               = core.DeriveGrouping
	FirstNonEmpty                = core.FirstNonEmpty
	DBPlatform                   = core.DBPlatform
	QueuePlatform                = core.QueuePlatform
	OutboundInstance             = core.OutboundInstance
	NormalizeOperationKind       = core.NormalizeOperationKind
	SanitizeGroup                = core.SanitizeGroup
	SemanticEntityKey            = core.SemanticEntityKey
	ContainingName               = core.ContainingName
	SortLLMEntities              = core.SortLLMEntities
)

// schemas.go
var (
	EntitySchema       = core.EntitySchema
	EntityListSchema   = core.EntityListSchema
	EntitySingleSchema = core.EntitySingleSchema
	ConditionSchema    = core.ConditionSchema
	RepoFactsSchema    = core.RepoFactsSchema
)

// prompts.go
var (
	BuildRepoFactsPrompt    = core.BuildRepoFactsPrompt
	RepoFactsBlock          = core.RepoFactsBlock
	MonorepoScopeLine       = core.MonorepoScopeLine
	AstHintsBlock           = core.AstHintsBlock
	DiscoveryScopeBlock     = core.DiscoveryScopeBlock
	ExampleBlock            = core.ExampleBlock
	DetailKeysLine          = core.DetailKeysLine
	DetectedLanguageSet     = core.DetectedLanguageSet
	CanonicalLanguage       = core.CanonicalLanguage
	ScopeFrameworkPatterns  = core.ScopeFrameworkPatterns
	BulletLanguages         = core.BulletLanguages
	BuildDiscoveryPrompt    = core.BuildDiscoveryPrompt
	ConfirmedDiscoveryBlock = core.ConfirmedDiscoveryBlock
	BuildReexaminePrompt    = core.BuildReexaminePrompt
	BuildDetailPrompt       = core.BuildDetailPrompt
	BuildDetailBatchPrompt  = core.BuildDetailBatchPrompt
	Itoa                    = core.Itoa
)

// grouping.go
var (
	DetailGroups        = detailstage.DetailGroups
	PartitionByAffinity = detailstage.PartitionByAffinity
	BatchAffinity       = detailstage.BatchAffinity
	AffinityScore       = detailstage.AffinityScore
	PrimaryFile         = detailstage.PrimaryFile
	CommonPrefixLen     = detailstage.CommonPrefixLen
	TokenOverlap        = detailstage.TokenOverlap
	NameTokens          = detailstage.NameTokens
)

// config_resolve.go
var (
	ResolveResourceName     = discoverystage.ResolveResourceName
	IsPlaceholder           = discoverystage.IsPlaceholder
	SplitPlaceholder        = discoverystage.SplitPlaceholder
	ResolvePlaceholder      = discoverystage.ResolvePlaceholder
	ConfigValue             = discoverystage.ConfigValue
	TrailingResourceSegment = discoverystage.TrailingResourceSegment
	KeySegmentName          = discoverystage.KeySegmentName
)

// identity.go (shared identity / detail-derivation helpers)
var (
	shouldReexamine             = core.ShouldReexamine
	missingRequiredDetails      = core.MissingRequiredDetails
	deriveDetailsFromName       = core.DeriveDetailsFromName
	splitMethodPath             = core.SplitMethodPath
	splitServiceMethod          = core.SplitServiceMethod
	looksLikeIdentifier         = core.LooksLikeIdentifier
	looksLikeCommand            = core.LooksLikeCommand
	guessOperation              = core.GuessOperation
	extractCronLike             = core.ExtractCronLike
	hasDetailKey                = core.HasDetailKey
	discoverySemanticKey        = core.DiscoverySemanticKey
	normalizePathForKey         = core.NormalizePathForKey
	isCompleteDeterministicSeed = core.IsCompleteDeterministicSeed
	hasDeterministicEvidence    = core.HasDeterministicEvidence
	shardEntityKey              = core.ShardEntityKey
	httpMethods                 = core.HTTPMethods
)

// paths.go
var NewPathMapper = core.NewPathMapper

// failure.go
var (
	ClassifyError          = llmrun.ClassifyError
	ExtractHTTPStatus      = llmrun.ExtractHTTPStatus
	ShouldReportHTTPStatus = llmrun.ShouldReportHTTPStatus
	IsAuthFailure          = llmrun.IsAuthFailure
	IsQuotaFailure         = llmrun.IsQuotaFailure
	NewStuckError          = llmrun.NewStuckError
)

// progress.go
var (
	NewProgressReporter = core.NewProgressReporter
	RenderProgressBar   = core.RenderProgressBar
)

// resilience (watchdog / liveness / bridges)
type (
	watchdog              = llmrun.Watchdog
	livenessConfig        = llmrun.LivenessConfig
	livenessReport        = llmrun.LivenessReport
	livenessProbe         = llmrun.LivenessProbe
	aborter               = llmrun.Aborter
	probeSnapshot         = llmrun.ProbeSnapshot
	livenessClient        = llmrun.LivenessClient
	livenessAborter       = llmrun.LivenessAborter
	openCodeLivenessProbe = llmrun.OpenCodeLivenessProbe
	openCodeAborter       = llmrun.OpenCodeAborter
	pauseBridge           = llmrun.PauseBridge
	verboseBridge         = llmrun.VerboseBridge
	tokenBridge           = llmrun.TokenBridge
	permissionDecision    = llmrun.PermissionDecision
)

var (
	newWatchdog              = llmrun.NewWatchdog
	runLiveness              = llmrun.RunLiveness
	decidePermission         = llmrun.DecidePermission
	newOpenCodeLivenessProbe = llmrun.NewOpenCodeLivenessProbe
	newOpenCodeAborter       = llmrun.NewOpenCodeAborter
	newPauseBridge           = llmrun.NewPauseBridge
	newVerboseBridge         = llmrun.NewVerboseBridge
	newTokenBridge           = llmrun.NewTokenBridge
)
