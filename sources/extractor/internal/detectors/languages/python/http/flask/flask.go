package flask

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Flask (Python)

type detector struct{}

func (d *detector) Name() string { return "flask" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	prefixes := flaskBlueprintPrefixes(idx)
	for _, fa := range idx.Files {
		if fa.Language != "python" {
			continue
		}
		for _, sym := range fa.Symbols {
			for _, ann := range sym.Annotations {
				if b := flaskAnnotationToBinding(sym, ann, prefixes); b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func flaskAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation, prefixes map[string]string) *ast.FrameworkBinding {
	receiver, method := splitDecoratorName(ann.Name)
	if receiver == "" || !flaskRouteReceiver(receiver) {
		return nil
	}
	method = strings.ToLower(method)
	if method != "route" {
		return nil
	}
	path := frameworkutil.ExtractFirstStringArg(ann.Arguments)
	if path == "" {
		return nil
	}
	prefix := flaskBlueprintPrefix(prefixes, receiver)
	if prefix != "" {
		path = frameworkutil.JoinPath(prefix, path)
	}
	httpMethod := strings.ToUpper(method)
	if method == "route" {
		httpMethod = flaskRouteMethod(ann.Arguments)
	}
	reason := "flask_decorator_literal_path"
	if prefix != "" {
		reason = "flask_decorator_literal_path_blueprint_prefix"
	}
	return &ast.FrameworkBinding{
		Framework:        "flask",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           sym.Qualified,
		Trigger:          httpMethod + " " + path,
		TriggerSource:    "@" + ann.Name + "(" + ann.Arguments + ")",
		File:             sym.File,
		Range:            sym.Range,
		ConfidenceReason: reason,
	}
}

func flaskBlueprintPrefixes(idx *ast.ProjectIndex) map[string]string {
	out := map[string]string{}
	if idx == nil {
		return out
	}
	for _, fa := range idx.Files {
		if fa.Language != "python" {
			continue
		}
		for _, call := range fa.Calls {
			callee := strings.TrimSpace(call.CalleeRaw)
			if callee != "register_blueprint" && !strings.HasSuffix(callee, ".register_blueprint") {
				continue
			}
			if len(call.Arguments) == 0 {
				continue
			}
			blueprint := strings.TrimSpace(call.Arguments[0].Source)
			if blueprint == "" || strings.ContainsAny(blueprint, "({[ \t\n") {
				continue
			}
			if prefix := flaskRegisterBlueprintURLPrefix(call.Arguments); prefix != "" {
				out[blueprint] = prefix
			}
		}
	}
	return out
}

func flaskRegisterBlueprintURLPrefix(args []ast.ArgumentExpr) string {
	for _, arg := range args[1:] {
		src := strings.TrimSpace(arg.Source)
		if !strings.HasPrefix(src, "url_prefix") {
			continue
		}
		eq := strings.Index(src, "=")
		if eq < 0 {
			continue
		}
		value := strings.TrimSpace(src[eq+1:])
		if !(strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") || strings.HasPrefix(value, "`")) {
			continue
		}
		prefix := strings.Trim(strings.TrimSpace(value), `"'`+"`")
		if strings.HasPrefix(prefix, "/") {
			return prefix
		}
	}
	return ""
}

func flaskBlueprintPrefix(prefixes map[string]string, receiver string) string {
	if len(prefixes) == 0 {
		return ""
	}
	receiver = strings.TrimSpace(receiver)
	if p := prefixes[receiver]; p != "" {
		return p
	}
	if dot := strings.LastIndex(receiver, "."); dot >= 0 {
		return prefixes[receiver[dot+1:]]
	}
	return ""
}

func splitDecoratorName(name string) (receiver, method string) {
	name = strings.TrimSpace(name)
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return "", name
	}
	return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
}

func flaskRouteReceiver(receiver string) bool {
	receiver = strings.ToLower(strings.TrimSpace(receiver))
	if receiver == "app" || receiver == "application" {
		return true
	}
	if strings.Contains(receiver, "blueprint") || strings.HasSuffix(receiver, "_bp") || strings.HasSuffix(receiver, "bp") {
		return true
	}
	for _, r := range receiver {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return receiver != ""
}

func flaskRouteMethod(args string) string {
	named, _, _ := frameworkutil.ParseAnnotationArgs(args)
	if raw := named["methods"]; raw != "" {
		for _, v := range frameworkutil.ExtractStringArgs(raw) {
			m := strings.ToUpper(strings.TrimSpace(v))
			switch m {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				return m
			}
		}
	}
	return "GET"
}
