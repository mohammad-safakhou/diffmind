// Package register imports all built-in deterministic detector packages.
package register

import (
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/dotnet/http/aspnet"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/ai/openai"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/di/wire"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/http/echo"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/http/fiber"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/http/gin"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/http/nethttp"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/golang/rpc/grpc"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/http/spring"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/httpclient/feign"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/httpclient/retrofit"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/jms"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/kafka"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/rabbitmq"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/sqs"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/javascript/http/express"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/php/http/laravel"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/python/http/django"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/python/http/fastapi"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/python/http/flask"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/ruby/http/rails"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/typescript/http/nestjs"
)
