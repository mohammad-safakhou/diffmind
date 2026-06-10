package agents

import "github.com/mohammad-safakhou/diffmind/internal/agents/core"

// transitional shims so the orchestrator keeps terse names; stage packages use
// core.* directly.

// types
type (
	PathMapper       = core.PathMapper
	StuckError       = core.StuckError
	ProgressReporter = core.ProgressReporter
)

// sentinel
var ErrStuck = core.ErrStuck

// grouping.go consts (referenced by agents tests)
const detailBatchHardCap = core.DetailBatchHardCap

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
	DetailGroups        = core.DetailGroups
	PartitionByAffinity = core.PartitionByAffinity
	BatchAffinity       = core.BatchAffinity
	AffinityScore       = core.AffinityScore
	PrimaryFile         = core.PrimaryFile
	CommonPrefixLen     = core.CommonPrefixLen
	TokenOverlap        = core.TokenOverlap
	NameTokens          = core.NameTokens
)

// config_resolve.go
var (
	ResolveResourceName     = core.ResolveResourceName
	IsPlaceholder           = core.IsPlaceholder
	SplitPlaceholder        = core.SplitPlaceholder
	ResolvePlaceholder      = core.ResolvePlaceholder
	ConfigValue             = core.ConfigValue
	TrailingResourceSegment = core.TrailingResourceSegment
	KeySegmentName          = core.KeySegmentName
)

// paths.go
var NewPathMapper = core.NewPathMapper

// failure.go
var (
	ClassifyError          = core.ClassifyError
	ExtractHTTPStatus      = core.ExtractHTTPStatus
	ShouldReportHTTPStatus = core.ShouldReportHTTPStatus
	IsAuthFailure          = core.IsAuthFailure
	IsQuotaFailure         = core.IsQuotaFailure
	NewStuckError          = core.NewStuckError
)

// progress.go
var (
	NewProgressReporter = core.NewProgressReporter
	RenderProgressBar   = core.RenderProgressBar
)
