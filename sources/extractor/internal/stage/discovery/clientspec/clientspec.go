// Package clientspec is the declarative registry of connection-client patterns:
// how to recognize a client DEFINITION in source and where its instance comes
// from. It is intentionally pure data — adding a framework/client is a table row,
// not new code — and is interpreted by the AST client floor in the discovery
// stage (discovery.DetectClients).
//
// Honest scope: this table expresses the common, reliably-matchable definitions
// (an annotation, an implemented interface, or a type-name suffix, plus a config
// key or annotation attribute for the instance). Construction-only clients (a raw
// SDK builder with no annotation/interface — e.g. SqsClient.builder()...build())
// are deliberately out of scope here and remain covered by the LLM
// connection_client objective; a future typed extractor hook can extend this.
package clientspec

// Pattern declaratively recognizes a client definition. A symbol matches when ANY
// set definition rule (Annotation / ImplementsAny / SymbolSuffix) matches; the
// instance source is ConfigAnchorConst (a fixed property key) or ConfigAnchorAttr
// (a value read from the matched annotation, which may be a property key, a
// ${placeholder}, or a literal endpoint).
type Pattern struct {
	Name      string
	Kind      string // db | http | queue | cache | stream
	Framework string

	Annotation    string   // symbol carries this annotation (e.g. "FeignClient", "Repository")
	ImplementsAny []string // symbol implements one of these interfaces (e.g. "JpaRepository")
	SymbolSuffix  string   // symbol/type name ends with this (e.g. "Repository", "DataSource")

	ConfigAnchorConst string // fixed config property key (e.g. "spring.datasource.url")
	ConfigAnchorAttr  string // anchor/url taken from this annotation attribute (e.g. FeignClient "url")
}

// defaultPatterns is the registry. Order is not significant (detection dedups by
// kind+symbol). Add a row to teach the floor a new client; no code changes.
var defaultPatterns = []Pattern{
	// Spring Data: a repository interface (by @Repository or a *Repository name or
	// extending a Spring Data base) shares the default DataSource.
	{Name: "spring-repository-annotation", Kind: "db", Framework: "spring-data", Annotation: "Repository", ConfigAnchorConst: "spring.datasource.url"},
	{Name: "spring-data-repository-iface", Kind: "db", Framework: "spring-data", SymbolSuffix: "Repository", ConfigAnchorConst: "spring.datasource.url"},
	{Name: "spring-data-base-iface", Kind: "db", Framework: "spring-data", ImplementsAny: []string{"JpaRepository", "CrudRepository", "PagingAndSortingRepository", "ReactiveCrudRepository"}, ConfigAnchorConst: "spring.datasource.url"},
	{Name: "spring-data-mongo-repository", Kind: "db", Framework: "spring-data", ImplementsAny: []string{"MongoRepository", "ReactiveMongoRepository"}, ConfigAnchorConst: "spring.data.mongodb.uri"},
	// Feign HTTP client: the url attribute gives the instance (key/placeholder/literal).
	{Name: "feign-client", Kind: "http", Framework: "feign", Annotation: "FeignClient", ConfigAnchorAttr: "url"},
}

// Patterns returns a copy of the registry.
func Patterns() []Pattern {
	out := make([]Pattern, len(defaultPatterns))
	copy(out, defaultPatterns)
	return out
}
