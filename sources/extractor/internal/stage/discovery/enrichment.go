package discovery

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// enrichment.go provides deterministic recovery of high-value per-entity
// context directly from the AST.
//
// Two invariants govern it:
//   - Additive (#4): only fills a detail that is empty; never overwrites a value
//     discovery already established. Safe to run in every mode (idempotent).
//   - High-precision (#6): recognises only canonical security annotations, so a
//     wrong fact is never stamped. Prefer "stamp nothing" over a guess.

// authAnnotations maps a recognised security-annotation simple name (lower-case,
// last dotted segment) to whether it REQUIRES authentication. Deliberately small
// so every stamped fact is correct; add framework-specific names only once
// confirmed.
var authAnnotations = map[string]bool{
	"preauthorize":  true,  // Spring Security
	"postauthorize": true,  // Spring Security
	"secured":       true,  // Spring Security
	"rolesallowed":  true,  // JSR-250 / JAX-RS
	"denyall":       true,  // JSR-250 / JAX-RS
	"permitall":     false, // JSR-250 / JAX-RS — explicitly public
}

// EnrichExposuresFromAnnotations stamps details.auth (rendered "Name(args)") and
// details.authenticated on each exposure from its handler symbol's security
// annotations in the AST index. It is safe to run unconditionally because it
// does not overwrite a field discovery already populated.
func EnrichExposuresFromAnnotations(idx *astpkg.ProjectIndex, exposures []model.Exposure) {
	if idx == nil {
		return
	}
	for i := range exposures {
		e := &exposures[i].BaseEntity
		if e.Details != nil {
			if _, ok := e.Details["auth"]; ok {
				continue // discovery (or a prior pass) already set it
			}
		}
		desc, requires, found := handlerAuthAnnotation(idx, e.Locations)
		if !found {
			continue
		}
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		e.Details["auth"] = desc
		e.Details["authenticated"] = requires
	}
}

// routeInputTypes are the exposure types whose IO contract we recover from the
// handler signature.
var routeInputTypes = map[string]bool{"http_route": true, "webhook": true, "rpc_endpoint": true}

// EnrichExposuresFromParams recovers an exposure's IO contract (Inputs) from its
// handler's formal parameters in the AST. Only parameters with a recognised request
// binding annotation (Spring @PathVariable/@RequestParam/@RequestBody/...) are
// emitted, so the fact is high-precision (infrastructure params like
// HttpServletRequest are skipped). Additive: skips exposures that already carry
// Inputs.
func EnrichExposuresFromParams(idx *astpkg.ProjectIndex, exposures []model.Exposure) {
	if idx == nil {
		return
	}
	for i := range exposures {
		e := &exposures[i].BaseEntity
		if !routeInputTypes[e.Type] || len(e.Inputs) > 0 {
			continue
		}
		sym, ok := handlerSymbol(idx, e.Locations)
		if !ok || len(sym.Parameters) == 0 {
			continue
		}
		var inputs []model.InputSpec
		for _, p := range sym.Parameters {
			if in, ok := paramInput(p); ok {
				inputs = append(inputs, in)
			}
		}
		if len(inputs) > 0 {
			e.Inputs = inputs
		}
	}
}

// EnrichHTTPContractsFromHandlers recovers HTTP request/response details from
// the concrete handler symbol. This catches Go frameworks like Echo where the
// route registration and handler body live in different files, and where the
// most precise contract is often in Swagger/OpenAPI comments above the handler.
func EnrichHTTPContractsFromHandlers(idx *astpkg.ProjectIndex, exposures []model.Exposure) {
	if idx == nil {
		return
	}
	for i := range exposures {
		e := &exposures[i].BaseEntity
		if !routeInputTypes[e.Type] {
			continue
		}
		sym, ok := handlerSymbolForExposure(idx, e)
		if !ok || sym.File == "" {
			continue
		}
		comments := leadingCommentBlock(idx, sym)
		if len(e.Inputs) == 0 {
			if inputs := swaggerInputs(comments); len(inputs) > 0 {
				e.Inputs = inputs
			}
		}
		if !hasResponseDetails(e.Details) {
			if responses := swaggerResponses(comments); len(responses) > 0 {
				if e.Details == nil {
					e.Details = map[string]any{}
				}
				e.Details["responses"] = responses
			}
		}
	}
}

func handlerSymbolForExposure(idx *astpkg.ProjectIndex, e *model.BaseEntity) (astpkg.SymbolDef, bool) {
	if e == nil {
		return astpkg.SymbolDef{}, false
	}
	if handler, _ := e.Details["handler"].(string); strings.TrimSpace(handler) != "" {
		if sym, ok := resolveHandlerSymbol(idx, handler, e.Locations); ok {
			return sym, true
		}
	}
	return handlerSymbol(idx, e.Locations)
}

func resolveHandlerSymbol(idx *astpkg.ProjectIndex, handler string, locs []model.Location) (astpkg.SymbolDef, bool) {
	method := lastSymbolSegment(handler)
	if method == "" {
		return astpkg.SymbolDef{}, false
	}
	preferredDir := ""
	if len(locs) > 0 {
		preferredDir = filepath.ToSlash(filepath.Dir(locs[0].File))
	}
	var fallback astpkg.SymbolDef
	found := false
	for _, fa := range idx.Files {
		if fa == nil || fa.Language != "go" {
			continue
		}
		for _, sym := range fa.Symbols {
			if sym.Kind != astpkg.SymbolKindMethod && sym.Kind != astpkg.SymbolKindFunction {
				continue
			}
			if sym.Name != method && !strings.HasSuffix(sym.Qualified, "."+method) {
				continue
			}
			if preferredDir != "" && filepath.ToSlash(filepath.Dir(sym.File)) == preferredDir {
				return sym, true
			}
			if !found {
				fallback, found = sym, true
			}
		}
	}
	return fallback, found
}

func lastSymbolSegment(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "*"))
	if s == "" {
		return ""
	}
	for _, sep := range []string{".", "#", "/"} {
		if i := strings.LastIndex(s, sep); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.TrimSpace(strings.Trim(s, "()"))
}

func leadingCommentBlock(idx *astpkg.ProjectIndex, sym astpkg.SymbolDef) []string {
	if idx.RepoRoot == "" || sym.File == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, sym.File))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	start := int(sym.Range.StartLine)
	if start > len(lines) {
		start = len(lines)
	}
	var out []string
	for i := start - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			if len(out) == 0 {
				continue
			}
			break
		}
		if !strings.HasPrefix(line, "//") {
			break
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "//"))
		out = append([]string{text}, out...)
	}
	return out
}

func swaggerInputs(comments []string) []model.InputSpec {
	var out []model.InputSpec
	for _, line := range comments {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "@Param" {
			continue
		}
		in := strings.ToLower(fields[2])
		switch in {
		case "path", "query", "header", "body", "formdata", "form":
		default:
			continue
		}
		typ := fields[3]
		required := strings.EqualFold(fields[4], "true") || strings.EqualFold(fields[4], "required")
		out = append(out, model.InputSpec{
			Name:        fields[1],
			Type:        typ,
			Required:    required,
			Description: normalizeSwaggerInputLocation(in),
		})
	}
	return out
}

func normalizeSwaggerInputLocation(in string) string {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "formdata":
		return "form"
	default:
		return in
	}
}

func swaggerResponses(comments []string) []map[string]any {
	var out []map[string]any
	seen := map[int]struct{}{}
	for _, line := range comments {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kind := fields[0]
		if kind != "@Success" && kind != "@Failure" && kind != "@Response" {
			continue
		}
		code, err := strconv.Atoi(fields[1])
		if err != nil || code <= 0 {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		resp := map[string]any{"status": code, "content_type": "application/json"}
		if kind == "@Failure" || code >= 400 {
			resp["error"] = defaultHTTPErrorName(code)
		}
		if len(fields) >= 4 {
			resp["schema"] = map[string]any{"type": swaggerSchemaKind(fields[2]), "name": fields[3]}
		}
		out = append(out, resp)
	}
	return out
}

func swaggerSchemaKind(token string) string {
	token = strings.Trim(token, "{}")
	switch strings.ToLower(token) {
	case "array":
		return "array"
	case "object":
		return "object"
	case "string", "integer", "number", "boolean":
		return strings.ToLower(token)
	default:
		return "object"
	}
}

func defaultHTTPErrorName(code int) string {
	switch code {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 422:
		return "validation_error"
	case 500:
		return "internal_error"
	default:
		return "error"
	}
}

func hasResponseDetails(d map[string]any) bool {
	if len(d) == 0 {
		return false
	}
	for _, key := range []string{"responses", "response_status", "status"} {
		if _, ok := d[key]; ok {
			return true
		}
	}
	return false
}

// handlerSymbol returns the function/method symbol enclosing the first of the
// entity's locations that resolves to one.
func handlerSymbol(idx *astpkg.ProjectIndex, locs []model.Location) (astpkg.SymbolDef, bool) {
	for _, loc := range locs {
		fa := idx.Files[loc.File]
		if fa == nil {
			continue
		}
		if sym, ok := enclosingSymbol(fa, loc.StartLine); ok {
			return sym, true
		}
	}
	return astpkg.SymbolDef{}, false
}

// paramInput maps one handler parameter to an InputSpec when it carries a
// recognised request-binding annotation; the Description records the binding
// location (path/query/body/header/form/cookie).
func paramInput(p astpkg.Param) (model.InputSpec, bool) {
	for _, a := range p.Annotations {
		in, required, ok := bindingFromAnnotation(annLastSegment(strings.ToLower(strings.TrimSpace(a.Name))), a.Arguments)
		if !ok {
			continue
		}
		return model.InputSpec{
			Name:        boundParamName(a.Arguments, p.Name),
			Type:        strings.TrimSpace(p.Type),
			Required:    required,
			Description: in,
		}, true
	}
	return model.InputSpec{}, false
}

// bindingFromAnnotation classifies a Spring request-binding annotation. Path
// variables are always required; the rest default to required unless the
// annotation carries required=false.
func bindingFromAnnotation(name, args string) (in string, required, ok bool) {
	switch name {
	case "pathvariable":
		return "path", true, true
	case "requestparam":
		return "query", !argsRequiredFalse(args), true
	case "requestbody":
		return "body", !argsRequiredFalse(args), true
	case "requestheader":
		return "header", !argsRequiredFalse(args), true
	case "requestpart":
		return "form", !argsRequiredFalse(args), true
	case "cookievalue":
		return "cookie", !argsRequiredFalse(args), true
	}
	return "", false, false
}

func argsRequiredFalse(args string) bool {
	return strings.Contains(strings.ReplaceAll(strings.ToLower(args), " ", ""), "required=false")
}

// boundParamName returns the explicit binding name (first quoted token in the
// annotation args, e.g. @RequestParam("userId")) or the declared parameter name.
func boundParamName(args, fallback string) string {
	if i := strings.IndexByte(args, '"'); i >= 0 {
		if j := strings.IndexByte(args[i+1:], '"'); j >= 0 {
			if q := strings.TrimSpace(args[i+1 : i+1+j]); q != "" {
				return q
			}
		}
	}
	return fallback
}

// handlerAuthAnnotation finds the function/method symbol enclosing any of the
// entity's locations and returns its first recognised security annotation
// (rendered "Name(args)"), whether it requires auth, and whether one was found.
func handlerAuthAnnotation(idx *astpkg.ProjectIndex, locs []model.Location) (desc string, requires, found bool) {
	for _, loc := range locs {
		fa := idx.Files[loc.File]
		if fa == nil {
			continue
		}
		sym, ok := enclosingSymbol(fa, loc.StartLine)
		if !ok {
			continue
		}
		for _, ann := range sym.Annotations {
			key := annLastSegment(strings.ToLower(strings.TrimSpace(ann.Name)))
			req, recognised := authAnnotations[key]
			if !recognised {
				continue
			}
			return renderAnnotation(ann), req, true
		}
	}
	return "", false, false
}

// enclosingSymbol returns the smallest function/method symbol in fa whose line
// range — or one of its annotation ranges — contains line (1-based). The
// annotation-range check matters because an exposure's reported location often
// points at the routing annotation, which sits just above the method's own
// declaration range. SymbolDef/Annotation ranges are tree-sitter 0-based, so we
// compare against +1.
func enclosingSymbol(fa *astpkg.FileAST, line int) (astpkg.SymbolDef, bool) {
	var best astpkg.SymbolDef
	bestSpan := 1 << 30
	found := false
	for _, s := range fa.Symbols {
		switch s.Kind {
		case astpkg.SymbolKindMethod, astpkg.SymbolKindFunction:
		default:
			continue
		}
		start := int(s.Range.StartLine) + 1
		end := int(s.Range.EndLine) + 1
		covers := line >= start && line <= end
		if !covers {
			for _, ann := range s.Annotations {
				as := int(ann.Range.StartLine) + 1
				ae := int(ann.Range.EndLine) + 1
				if line >= as && line <= ae {
					covers = true
					break
				}
			}
		}
		if !covers {
			continue
		}
		if span := end - start; span < bestSpan {
			best, bestSpan, found = s, span, true
		}
	}
	return best, found
}

func renderAnnotation(ann astpkg.Annotation) string {
	name := strings.TrimSpace(ann.Name)
	if args := strings.TrimSpace(ann.Arguments); args != "" {
		return name + "(" + args + ")"
	}
	return name
}

func annLastSegment(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
