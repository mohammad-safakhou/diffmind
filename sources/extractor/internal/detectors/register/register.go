// Package register imports all built-in deterministic detector packages.
package register

import (
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/dotnet/http/aspnet"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/golang/http/echo"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/golang/http/fiber"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/golang/http/gin"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/golang/http/nethttp"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/golang/rpc/grpc"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/java/http/spring"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/java/httpclient/feign"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/java/httpclient/retrofit"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/javascript/http/express"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/php/http/laravel"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/python/http/django"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/python/http/fastapi"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/python/http/flask"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/ruby/http/rails"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/typescript/http/nestjs"
)
