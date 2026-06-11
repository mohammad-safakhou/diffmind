package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	detailstage "github.com/mohammad-safakhou/diffmind/internal/stage/detail"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	reexaminestage "github.com/mohammad-safakhou/diffmind/internal/stage/reexamine"
	"github.com/mohammad-safakhou/diffmind/internal/stage/repofacts"
)

// Package-local aliases keep orchestration code concise while domain and stage
// packages retain descriptive exported names.

// types
type (
	StuckError     = llmrun.StuckError
	PathMapper     = extraction.PathMapper
	objectiveHints = extraction.ObjectiveHints
	symbolHint     = extraction.SymbolHint
	bindingHint    = extraction.BindingHint
	configHint     = extraction.ConfigHint
)

// sentinel
var ErrStuck = llmrun.ErrStuck

// grouping.go consts (referenced by agents tests)
const detailBatchHardCap = detailstage.DetailBatchHardCap
const maxSymbolHints = discoverystage.MaxSymbolHints
const discoveryShardHardCap = discoverystage.DiscoveryShardHardCap

// convert.go
var (
	ToBase            = extraction.ToBase
	ToLocations       = extraction.ToLocations
	ToEvidence        = extraction.ToEvidence
	FillCondition     = extraction.FillCondition
	DefaultStr        = extraction.DefaultStr
	ErrString         = extraction.ErrString
	SafeJobID         = extraction.SafeJobID
	DedupeUnresolved  = extraction.DedupeUnresolved
	DedupeStrings     = extraction.DedupeStrings
	ParseEntities     = extraction.ParseEntities
	ParseSingleEntity = extraction.ParseSingleEntity
	ParseRepoFacts    = extraction.ParseRepoFacts
)

// classify.go
var (
	CanonicalObjectiveType       = extraction.CanonicalObjectiveType
	NormalizeType                = extraction.NormalizeType
	ForceObjectiveType           = extraction.ForceObjectiveType
	EntitySchemaForObjective     = extraction.EntitySchemaForObjective
	EntityListSchemaForObjective = extraction.EntityListSchemaForObjective
	EnrichEntityGrouping         = extraction.EnrichEntityGrouping
	DeriveGrouping               = extraction.DeriveGrouping
	FirstNonEmpty                = extraction.FirstNonEmpty
	DBPlatform                   = extraction.DBPlatform
	QueuePlatform                = extraction.QueuePlatform
	OutboundInstance             = extraction.OutboundInstance
	NormalizeOperationKind       = extraction.NormalizeOperationKind
	SanitizeGroup                = extraction.SanitizeGroup
	SemanticEntityKey            = extraction.SemanticEntityKey
	ContainingName               = extraction.ContainingName
	SortLLMEntities              = extraction.SortLLMEntities
)

// schemas.go
var (
	EntitySchema       = extraction.EntitySchema
	EntityListSchema   = extraction.EntityListSchema
	EntitySingleSchema = extraction.EntitySingleSchema
	ConditionSchema    = extraction.ConditionSchema
	RepoFactsSchema    = repofacts.Schema
)

// prompts.go
var (
	BuildRepoFactsPrompt    = repofacts.BuildPrompt
	RepoFactsBlock          = extraction.RepoFactsBlock
	MonorepoScopeLine       = extraction.MonorepoScopeLine
	AstHintsBlock           = extraction.AstHintsBlock
	DiscoveryScopeBlock     = extraction.DiscoveryScopeBlock
	ExampleBlock            = extraction.ExampleBlock
	DetailKeysLine          = extraction.DetailKeysLine
	DetectedLanguageSet     = extraction.DetectedLanguageSet
	CanonicalLanguage       = extraction.CanonicalLanguage
	ScopeFrameworkPatterns  = extraction.ScopeFrameworkPatterns
	BulletLanguages         = extraction.BulletLanguages
	BuildDiscoveryPrompt    = extraction.BuildDiscoveryPrompt
	ConfirmedDiscoveryBlock = extraction.ConfirmedDiscoveryBlock
	BuildReexaminePrompt    = extraction.BuildReexaminePrompt
	BuildDetailPrompt       = extraction.BuildDetailPrompt
	BuildDetailBatchPrompt  = extraction.BuildDetailBatchPrompt
	Itoa                    = extraction.Itoa
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
	mergeEnrichment     = detailstage.MergeEnrichment
	pinIdentityDetails  = detailstage.PinIdentityDetails
	identityDetailKeys  = detailstage.IdentityDetailKeys
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
	buildObjectiveHints     = discoverystage.BuildObjectiveHints
	planDiscoveryShards     = discoverystage.PlanShards
	mergeShardEntities      = discoverystage.MergeShardEntities
	unionLocations          = discoverystage.UnionLocations
	distinctDirs            = discoverystage.DistinctDirs
)

type discoveryShard = discoverystage.Shard

// identity.go (shared identity / detail-derivation helpers)
var (
	shouldReexamine              = extraction.ShouldReexamine
	missingRequiredDetails       = extraction.MissingRequiredDetails
	deriveDetailsFromName        = extraction.DeriveDetailsFromName
	splitMethodPath              = extraction.SplitMethodPath
	splitServiceMethod           = extraction.SplitServiceMethod
	looksLikeIdentifier          = extraction.LooksLikeIdentifier
	looksLikeCommand             = extraction.LooksLikeCommand
	guessOperation               = extraction.GuessOperation
	extractCronLike              = extraction.ExtractCronLike
	hasDetailKey                 = extraction.HasDetailKey
	discoverySemanticKey         = extraction.DiscoverySemanticKey
	normalizePathForKey          = extraction.NormalizePathForKey
	isCompleteDeterministicSeed  = extraction.IsCompleteDeterministicSeed
	hasDeterministicEvidence     = extraction.HasDeterministicEvidence
	shardEntityKey               = extraction.ShardEntityKey
	httpMethods                  = extraction.HTTPMethods
	seedStructurallyUnverifiable = reexaminestage.SeedStructurallyUnverifiable
	downgradeConfidence          = reexaminestage.DowngradeConfidence
	appendUniqueTag              = reexaminestage.AppendUniqueTag
)

// paths.go
var NewPathMapper = extraction.NewPathMapper

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
