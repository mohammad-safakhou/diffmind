package detectors

import "sort"

var descriptors = []Descriptor{
	{ID: "golang.http.fiber", Language: LanguageGo, Category: CategoryHTTP, Tool: "fiber", ObjectTypes: []string{"http_endpoint"}, Description: "Go Fiber route registrations"},
	{ID: "golang.http.echo", Language: LanguageGo, Category: CategoryHTTP, Tool: "echo", ObjectTypes: []string{"http_endpoint"}, Description: "Go Echo route registrations"},
	{ID: "golang.http.gin", Language: LanguageGo, Category: CategoryHTTP, Tool: "gin", ObjectTypes: []string{"http_endpoint"}, Description: "Go Gin route registrations"},
	{ID: "golang.http.nethttp", Language: LanguageGo, Category: CategoryHTTP, Tool: "nethttp", ObjectTypes: []string{"http_endpoint"}, Description: "Go net/http route registrations"},
	{ID: "golang.httpclient.nethttp", Language: LanguageGo, Category: CategoryHTTPClient, Tool: "nethttp", ObjectTypes: []string{"http_call"}, Description: "Go net/http outbound calls"},
	{ID: "golang.httpclient.resty", Language: LanguageGo, Category: CategoryHTTPClient, Tool: "resty", ObjectTypes: []string{"http_call"}, Description: "Go Resty outbound calls"},
	{ID: "golang.db.bun", Language: LanguageGo, Category: CategoryDB, Tool: "bun", ObjectTypes: []string{"db_query", "db_resource"}, Description: "Go Bun ORM operations"},
	{ID: "golang.rpc.grpc", Language: LanguageGo, Category: CategoryRPC, Tool: "grpc", ObjectTypes: []string{"rpc_endpoint", "rpc_call"}, Description: "Go gRPC servers and clients"},
	{ID: "golang.cache.redis", Language: LanguageGo, Category: CategoryCache, Tool: "redis", ObjectTypes: []string{"cache_operation"}, Description: "Go Redis client operations"},
	{ID: "golang.cli.cobra", Language: LanguageGo, Category: CategoryCLI, Tool: "cobra", ObjectTypes: []string{"cli_command"}, Description: "Go Cobra command registrations"},

	{ID: "java.http.spring", Language: LanguageJava, Category: CategoryHTTP, Tool: "spring", ObjectTypes: []string{"http_endpoint"}, Description: "Spring MVC controllers and mappings"},
	{ID: "java.httpclient.feign", Language: LanguageJava, Category: CategoryHTTPClient, Tool: "feign", ObjectTypes: []string{"http_call"}, Description: "Spring Cloud OpenFeign clients"},
	{ID: "java.httpclient.retrofit", Language: LanguageJava, Category: CategoryHTTPClient, Tool: "retrofit", ObjectTypes: []string{"http_call"}, Description: "Retrofit annotated clients"},
	{ID: "java.db.jpa", Language: LanguageJava, Category: CategoryDB, Tool: "jpa", ObjectTypes: []string{"db_query", "db_resource"}, Description: "Spring Data and JPA repository operations"},
	{ID: "java.db.jdbc", Language: LanguageJava, Category: CategoryDB, Tool: "jdbc", ObjectTypes: []string{"db_query", "db_resource"}, Description: "JdbcTemplate and raw SQL operations"},
	{ID: "java.queue.kafka", Language: LanguageJava, Category: CategoryQueue, Tool: "kafka", ObjectTypes: []string{"queue_consumer", "queue_publisher"}, Description: "Kafka listeners and publishers"},
	{ID: "java.queue.sqs", Language: LanguageJava, Category: CategoryQueue, Tool: "sqs", ObjectTypes: []string{"queue_consumer", "queue_publisher"}, Description: "AWS SQS listeners and publishers"},
	{ID: "java.activation.spring", Language: LanguageJava, Category: CategoryActivation, Tool: "spring", ObjectTypes: []string{"activation"}, Description: "Spring scheduled jobs and event listeners"},
	{ID: "java.cache.spring", Language: LanguageJava, Category: CategoryCache, Tool: "spring", ObjectTypes: []string{"cache_operation"}, Description: "Spring cache operations"},

	{ID: "javascript.http.express", Language: LanguageJavaScript, Category: CategoryHTTP, Tool: "express", ObjectTypes: []string{"http_endpoint"}, Description: "Express route registrations"},
	{ID: "typescript.http.nestjs", Language: LanguageTypeScript, Category: CategoryHTTP, Tool: "nestjs", ObjectTypes: []string{"http_endpoint"}, Description: "NestJS controller decorators"},
	{ID: "python.http.flask", Language: LanguagePython, Category: CategoryHTTP, Tool: "flask", ObjectTypes: []string{"http_endpoint"}, Description: "Flask and Blueprint route decorators"},
	{ID: "python.http.fastapi", Language: LanguagePython, Category: CategoryHTTP, Tool: "fastapi", ObjectTypes: []string{"http_endpoint"}, Description: "FastAPI route decorators"},
	{ID: "python.http.django", Language: LanguagePython, Category: CategoryHTTP, Tool: "django", ObjectTypes: []string{"http_endpoint"}, Description: "Django URLconf routes"},
	{ID: "python.aws.sam", Language: LanguagePython, Category: CategoryAWS, Tool: "sam", ObjectTypes: []string{"activation", "queue_consumer"}, Description: "AWS SAM function and event-source definitions"},
	{ID: "python.cache.redis", Language: LanguagePython, Category: CategoryCache, Tool: "redis", ObjectTypes: []string{"cache_operation"}, Description: "redis-py operations"},
	{ID: "python.cli.argparse", Language: LanguagePython, Category: CategoryCLI, Tool: "argparse", ObjectTypes: []string{"cli_command"}, Description: "argparse command entrypoints"},
	{ID: "ruby.http.rails", Language: LanguageRuby, Category: CategoryHTTP, Tool: "rails", ObjectTypes: []string{"http_endpoint"}, Description: "Rails route declarations"},
	{ID: "php.http.laravel", Language: LanguagePHP, Category: CategoryHTTP, Tool: "laravel", ObjectTypes: []string{"http_endpoint"}, Description: "Laravel route declarations"},
	{ID: "dotnet.http.aspnet", Language: LanguageDotnet, Category: CategoryHTTP, Tool: "aspnet", ObjectTypes: []string{"http_endpoint"}, Description: "ASP.NET controller attributes"},
}

func All() []Descriptor {
	out := append([]Descriptor(nil), descriptors...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func Exists(id string) bool {
	_, ok := ByID(id)
	return ok
}

func ByID(id string) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.ID == id {
			return d, true
		}
	}
	return Descriptor{}, false
}
