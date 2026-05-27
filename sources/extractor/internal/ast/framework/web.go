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

// ── Express / Node.js ─────────────────────────────────────────────────────────

type expressDetector struct{}

func (d *expressDetector) Name() string { return "express" }

func (d *expressDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	// Express routes appear as call expressions: app.get('/path', handler)
	// or router.post('/path', handler). We detect them from the call graph.
	for caller, calls := range idx.CallGraph {
		for _, call := range calls {
			b := expressCallToBinding(caller, call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func expressCallToBinding(caller string, call ast.CallSite) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	methods := []string{"app.get", "app.post", "app.put", "app.patch", "app.delete",
		"router.get", "router.post", "router.put", "router.patch", "router.delete",
		"app.use", "router.use"}
	matched := ""
	for _, m := range methods {
		if raw == m || strings.HasSuffix(raw, "."+strings.SplitN(m, ".", 2)[1]) {
			matched = m
			break
		}
	}
	if matched == "" {
		return nil
	}
	parts := strings.SplitN(matched, ".", 2)
	method := strings.ToUpper(parts[1])
	path := ""
	if len(call.Arguments) > 0 {
		path = call.Arguments[0].Source
		path = strings.Trim(path, `"'` + "`")
	}
	return &ast.FrameworkBinding{
		Framework:     "express",
		Kind:          "http_handler",
		Symbol:        caller,
		Trigger:       method + " " + path,
		TriggerSource: raw + "(" + path + ", ...)",
		File:          call.File,
		Range:         call.Range,
	}
}

// ── FastAPI (Python) ──────────────────────────────────────────────────────────

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

// ── Django (Python) ───────────────────────────────────────────────────────────

type djangoDetector struct{}

func (d *djangoDetector) Name() string { return "django" }

func (d *djangoDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	// Django: annotate class-based views and function-based views in urls.py.
	for _, fa := range idx.Files {
		if fa.Language != "python" {
			continue
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

// ── Ruby on Rails ─────────────────────────────────────────────────────────────

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

// ── Laravel (PHP) ─────────────────────────────────────────────────────────────

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

// ── ASP.NET (C#) ──────────────────────────────────────────────────────────────

type aspnetDetector struct{}

func (d *aspnetDetector) Name() string { return "aspnet" }

func (d *aspnetDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "csharp" {
			continue
		}
		for _, sym := range fa.Symbols {
			for _, ann := range sym.Annotations {
				b := aspnetAnnotationToBinding(sym, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func aspnetAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	methods := map[string]string{
		"HttpGet": "GET", "HttpPost": "POST", "HttpPut": "PUT",
		"HttpPatch": "PATCH", "HttpDelete": "DELETE",
		"Route": "ANY",
	}
	if method, ok := methods[ann.Name]; ok {
		path := extractFirstStringArg(ann.Arguments)
		return &ast.FrameworkBinding{
			Framework:     "aspnet",
			Kind:          "http_handler",
			Symbol:        sym.Qualified,
			Trigger:       method + " " + path,
			TriggerSource: "[" + ann.Name + "(" + ann.Arguments + ")]",
			File:          sym.File,
			Range:         sym.Range,
		}
	}
	return nil
}

// ── Gin (Go) ──────────────────────────────────────────────────────────────────

type ginDetector struct{}

func (d *ginDetector) Name() string { return "gin" }

func (d *ginDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for caller, calls := range idx.CallGraph {
		for _, call := range calls {
			b := ginCallToBinding(caller, call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func ginCallToBinding(caller string, call ast.CallSite) *ast.FrameworkBinding {
	methods := map[string]string{
		"GET": "GET", "POST": "POST", "PUT": "PUT",
		"PATCH": "PATCH", "DELETE": "DELETE", "Handle": "ANY",
	}
	raw := call.CalleeRaw
	method, ok := methods[raw]
	if !ok {
		return nil
	}
	// Gin route calls: r.GET("/path", handler) or g.POST("/path", handler)
	path := ""
	if len(call.Arguments) > 0 {
		path = strings.Trim(call.Arguments[0].Source, `"` + "`")
	}
	return &ast.FrameworkBinding{
		Framework:     "gin",
		Kind:          "http_handler",
		Symbol:        caller,
		Trigger:       method + " " + path,
		TriggerSource: raw + "(" + path + ", ...)",
		File:          call.File,
		Range:         call.Range,
	}
}
