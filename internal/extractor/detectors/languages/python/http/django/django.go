package django

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Django (Python)

type detector struct{}

func (d *detector) Name() string { return "django" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
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
		Trigger:          "ANY " + frameworkutil.JoinPath("", route),
		TriggerSource:    call.CalleeRaw + "(" + call.Arguments[0].Source + ", " + view + ")",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "django_urlconf_literal_route",
	}
}
