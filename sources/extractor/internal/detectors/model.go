// Package detectors defines the deterministic detector catalog. User-facing
// detector IDs live here so configuration, tests, and detector implementations
// share one contract.
package detectors

type Language string

const (
	LanguageGo     Language = "golang"
	LanguageJava   Language = "java"
	LanguagePython Language = "python"
)

type Category string

const (
	CategoryActivation Category = "activation"
	CategoryAWS        Category = "aws"
	CategoryCache      Category = "cache"
	CategoryCLI        Category = "cli"
	CategoryDB         Category = "db"
	CategoryHTTP       Category = "http"
	CategoryHTTPClient Category = "httpclient"
	CategoryQueue      Category = "queue"
	CategoryRPC        Category = "rpc"
)

type Descriptor struct {
	ID          string
	Language    Language
	Category    Category
	Tool        string
	ObjectTypes []string
	Description string
}
