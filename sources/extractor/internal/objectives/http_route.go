package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objHTTPRoute = Objective{
	ID:          "exposure.http_route",
	Kind:        model.KindExposure,
	Type:        "http_route",
	Description: "HTTP REST/API routes exposed by the service (non-webhook)",
	DiscoveryPrompt: `Find ALL externally reachable HTTP API routes exposed by this service.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Boot: @RestController, @Controller, @RequestMapping, @GetMapping, @PostMapping, @PutMapping, @PatchMapping, @DeleteMapping
- JAX-RS: @Path, @GET, @POST, @PUT, @DELETE
- Node.js: Express router.get/post/put/delete, Fastify routes, NestJS @Get/@Post/@Controller
- Python: Flask @app.route, Django urlpatterns, FastAPI @app.get/@app.post
- Go: http.HandleFunc, mux.HandleFunc, gin.GET/POST, echo.GET/POST

FOR EACH ROUTE EXTRACT:
- HTTP method and path pattern (e.g., GET /v1/content-ranker)
- Handler class/function name and package
- Request input parameters (path params, query params, request body type)
- Authentication/authorization annotations (e.g., @PreAuthorize, @Secured)
- Response type

ALSO CHECK:
- Actuator/health/management endpoints (Spring Boot /actuator/*, /app/health)
- Swagger/OpenAPI documentation endpoints
- Debug/admin endpoints
- Infrastructure configuration files (helm values, *values.yaml, config/*.yaml) for ingress definitions that reveal exposed routes

Do NOT include webhook callback endpoints (those are a separate objective).
Include route path, HTTP method, handler symbol, request inputs, and validation entry points.`,
	ConnectionContext: "Prioritize ordered call-path mapping from HTTP route to downstream dependencies with conditions.",
}
