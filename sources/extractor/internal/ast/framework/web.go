package framework

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func init() {
	register(&expressDetector{})
	register(&fastApiDetector{})
	register(&djangoDetector{})
	register(&railsDetector{})
	register(&laravelDetector{})
	register(&aspnetDetector{})
	register(&ginDetector{})
}

// Express / Node.js

type expressDetector struct{}

func (d *expressDetector) Name() string { return "express" }

func (d *expressDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "javascript" && fa.Language != "typescript" && fa.Language != "tsx" && fa.Language != "jsx" {
			continue
		}
		for _, call := range fa.Calls {
			b := expressCallToBinding(call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func expressCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil
	}
	receiver, verb := parts[0], parts[1]
	if receiver != "app" && receiver != "router" {
		return nil
	}
	methods := map[string]string{
		"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE",
	}
	method, ok := methods[verb]
	if !ok || len(call.Arguments) < 2 || !isLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	path := literalPathArg(call.Arguments, 0)
	return &ast.FrameworkBinding{
		Framework:        "express",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           call.Caller,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + path + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "express_receiver_literal_path_handler",
	}
}

// FastAPI (Python)

type fastApiDetector struct{}

func (d *fastApiDetector) Name() string { return "fastapi" }

func (d *fastApiDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "python" {
			continue
		}
		for _, sym := range fa.Symbols {
			for _, ann := range sym.Annotations {
				b := fastAPIAnnotationToBinding(sym, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func fastAPIAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	methods := map[string]string{
		"get": "GET", "post": "POST", "put": "PUT",
		"patch": "PATCH", "delete": "DELETE", "api_route": "ANY",
	}
	// Annotations like @app.get("/path") or @router.post("/path")
	name := ann.Name
	// Strip receiver prefix.
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if method, ok := methods[name]; ok {
		path := extractFirstStringArg(ann.Arguments)
		return &ast.FrameworkBinding{
			Framework:     "fastapi",
			Kind:          "http_handler",
			Symbol:        sym.Qualified,
			Trigger:       method + " " + path,
			TriggerSource: "@" + ann.Name + "(" + ann.Arguments + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}
	return nil
}

// Django (Python)

type djangoDetector struct{}

func (d *djangoDetector) Name() string { return "django" }

func (d *djangoDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	// Django: annotate class-based views and function-based views in urls.py.
	for path, fa := range idx.Files {
		if fa.Language != "python" {
			continue
		}
		// Route table: path("orders/", views.order_list) / re_path / url calls,
		// recognized ONLY inside urls.py files — the call names are too generic
		// to trust anywhere else.
		if strings.HasSuffix(path, "urls.py") {
			for _, call := range fa.Calls {
				if b := djangoURLCallToBinding(call); b != nil {
					out = append(out, *b)
				}
			}
		}
		// Detect @receiver(signal) decorators for signal handlers.
		for _, sym := range fa.Symbols {
			for _, ann := range sym.Annotations {
				if ann.Name == "receiver" {
					out = append(out, ast.FrameworkBinding{
						Framework:     "django",
						Kind:          "event_listener",
						Symbol:        sym.Qualified,
						Trigger:       "signal: " + ann.Arguments,
						TriggerSource: "@receiver(" + ann.Arguments + ")",
						File:          sym.File,
						Range:         sym.Range,
					})
				}
				if ann.Name == "shared_task" || ann.Name == "task" || ann.Name == "app.task" {
					out = append(out, ast.FrameworkBinding{
						Framework:     "celery",
						Kind:          "scheduler",
						Symbol:        sym.Qualified,
						Trigger:       "celery task",
						TriggerSource: "@" + ann.Name,
						File:          sym.File,
						Range:         sym.Range,
					})
				}
			}
		}
	}
	return out
}

// djangoURLCallToBinding turns one urls.py route registration into a binding.
// The URLconf names the route but not the verb (the view dispatches), so the
// method is ANY. The view argument names the binding symbol so connections
// walk from the view function, not from the urlpatterns module.
func djangoURLCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	verb, prefix := call.CalleeRaw, ""
	if dot := strings.LastIndex(verb, "."); dot >= 0 {
		prefix = verb[:dot]
		verb = verb[dot+1:]
	}
	if verb != "path" && verb != "re_path" && verb != "url" {
		return nil
	}
	switch prefix {
	case "", "urls", "django.urls":
	default:
		return nil // os.path.* and friends share these names
	}
	if len(call.Arguments) < 2 {
		return nil
	}
	route := strings.TrimSpace(call.Arguments[0].Source)
	route = strings.TrimPrefix(route, "r") // raw-string literals: r"^orders/$"
	route = strings.Trim(route, `"'`)
	route = strings.Trim(route, "^$")
	if route == "" || strings.ContainsAny(route, "()\n") {
		return nil // regex groups / expressions: not a literal route
	}
	symbol := call.Caller
	view := strings.TrimSpace(call.Arguments[1].Source)
	if view != "" && !strings.ContainsAny(view, "({ ") {
		// "views.order_list" or "OrderView.as_view" → the view callable.
		symbol = strings.TrimSuffix(view, ".as_view")
	}
	return &ast.FrameworkBinding{
		Framework:        "django",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           symbol,
		Trigger:          "ANY " + joinPath("", route),
		TriggerSource:    call.CalleeRaw + "(" + call.Arguments[0].Source + ", " + view + ")",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "django_urlconf_literal_route",
	}
}

// Ruby on Rails

type railsDetector struct{}

func (d *railsDetector) Name() string { return "rails" }

func (d *railsDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	// Rails routes are defined in config/routes.rb. We detect them from
	// call expressions in that file.
	var out []ast.FrameworkBinding
	for path, fa := range idx.Files {
		if fa.Language != "ruby" {
			continue
		}
		if !strings.HasSuffix(path, "routes.rb") {
			continue
		}
		for _, call := range fa.Calls {
			method := strings.ToUpper(call.CalleeRaw)
			httpMethods := map[string]bool{"GET": true, "POST": true, "PUT": true,
				"PATCH": true, "DELETE": true, "RESOURCES": true, "RESOURCE": true}
			if !httpMethods[method] {
				continue
			}
			routePath := ""
			if len(call.Arguments) > 0 {
				routePath = strings.Trim(call.Arguments[0].Source, `"'`)
			}
			out = append(out, ast.FrameworkBinding{
				Framework:     "rails",
				Kind:          "http_handler",
				Symbol:        call.Caller,
				Trigger:       method + " " + routePath,
				TriggerSource: call.CalleeRaw + " " + routePath,
				File:          call.File,
				Range:         call.Range,
			})
		}
	}
	return out
}

// Laravel (PHP)

type laravelDetector struct{}

func (d *laravelDetector) Name() string { return "laravel" }

func (d *laravelDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for path, fa := range idx.Files {
		if fa.Language != "php" {
			continue
		}
		if !strings.Contains(path, "routes/") {
			continue
		}
		for _, call := range fa.Calls {
			raw := call.CalleeRaw
			methods := map[string]string{
				"Route.get": "GET", "Route.post": "POST", "Route.put": "PUT",
				"Route.patch": "PATCH", "Route.delete": "DELETE",
				"get": "GET", "post": "POST",
			}
			method, ok := methods[raw]
			if !ok {
				continue
			}
			routePath := ""
			if len(call.Arguments) > 0 {
				routePath = strings.Trim(call.Arguments[0].Source, `"'`)
			}
			out = append(out, ast.FrameworkBinding{
				Framework:     "laravel",
				Kind:          "http_handler",
				Symbol:        call.Caller,
				Trigger:       method + " " + routePath,
				TriggerSource: raw + "(" + routePath + ", ...)",
				File:          call.File,
				Range:         call.Range,
			})
		}
	}
	return out
}

// ASP.NET (C#)

type aspnetDetector struct{}

func (d *aspnetDetector) Name() string { return "aspnet" }

func (d *aspnetDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "csharp" {
			continue
		}
		classes := classesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := enclosingClassForSymbol(fa, sym, classes)
			for _, ann := range sym.Annotations {
				b := aspnetAnnotationToBinding(sym, cls, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func aspnetAnnotationToBinding(sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	methods := map[string]string{
		"HttpGet": "GET", "HttpPost": "POST", "HttpPut": "PUT",
		"HttpPatch": "PATCH", "HttpDelete": "DELETE",
	}
	if method, ok := methods[ann.Name]; ok {
		prefix := ""
		controller := false
		if cls != nil {
			controller = strings.HasSuffix(cls.Name, "Controller") || hasAnyAnnotation(*cls, "ApiController", "Controller")
			prefix = aspnetClassRoutePrefix(*cls)
		}
		path := joinPath(prefix, extractFirstStringArg(ann.Arguments))
		rejection := ""
		if !controller {
			rejection = "aspnet_http_attribute_without_controller_context"
		}
		return &ast.FrameworkBinding{
			Framework:        "aspnet",
			Kind:             "http_handler",
			Direction:        "inbound",
			Symbol:           sym.Qualified,
			Trigger:          method + " " + path,
			TriggerSource:    "[" + ann.Name + "(" + ann.Arguments + ")]",
			File:             sym.File,
			Range:            sym.Range,
			ConfidenceReason: "aspnet_controller_http_attribute",
			RejectionReason:  rejection,
		}
	}
	return nil
}

func aspnetClassRoutePrefix(cls ast.SymbolDef) string {
	for _, ann := range cls.Annotations {
		if ann.Name == "Route" {
			return extractFirstStringArg(ann.Arguments)
		}
	}
	return ""
}

// Gin (Go)

type ginDetector struct{}

func (d *ginDetector) Name() string { return "gin" }

func (d *ginDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		for _, call := range fa.Calls {
			b := ginCallToBinding(call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func ginCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	methods := map[string]string{
		"GET": "GET", "POST": "POST", "PUT": "PUT",
		"PATCH": "PATCH", "DELETE": "DELETE", "Handle": "ANY",
	}
	raw := call.CalleeRaw
	verb := raw
	if dot := strings.LastIndex(verb, "."); dot >= 0 {
		verb = verb[dot+1:]
	}
	method, ok := methods[verb]
	if !ok || len(call.Arguments) < 2 || !isLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	path := literalPathArg(call.Arguments, 0)
	return &ast.FrameworkBinding{
		Framework:        "gin",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           call.Caller,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + path + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_route_literal_path_handler",
	}
}
