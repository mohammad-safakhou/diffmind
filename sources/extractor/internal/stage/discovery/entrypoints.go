package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// DeterministicCLIEntrypoints emits high-precision process entrypoints that are
// not framework bindings: Python Lambda handlers, argparse scripts, and Java
// Spring Boot main launchers.
func DeterministicCLIEntrypoints(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	var out []candidate
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(idx.Files))
	for p := range idx.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fa := idx.Files[path]
		if fa == nil {
			continue
		}
		switch fa.Language {
		case "go":
			appendCobraCommands(idx, &out, seen, path, fa)
		case "python":
			for _, sym := range fa.Symbols {
				switch {
				case isPythonLambdaHandlerSymbol(idx, fa, sym):
					appendCLI(&out, seen, path, pythonHandlerName(sym), "AST-derived Python Lambda handler entrypoint", []string{"deterministic", "lambda", "python"}, map[string]any{
						"command":       pythonHandlerName(sym),
						"handler":       sym.Qualified,
						"entry_method":  sym.Qualified,
						"runtime":       "python",
						"platform":      "aws-lambda",
						"discovered_by": "ast_python_lambda_handler",
					}, sym, fmt.Sprintf("def %s(event, ...)", sym.Name))
				case isPythonArgparseEntrypoint(fa, sym):
					cmd := "python " + filepath.ToSlash(sym.File)
					appendCLI(&out, seen, path, filepath.ToSlash(sym.File), "AST-derived Python argparse command", []string{"deterministic", "argparse", "python"}, map[string]any{
						"command":       cmd,
						"handler":       sym.Qualified,
						"entry_method":  sym.Qualified,
						"runtime":       "python",
						"platform":      "process",
						"discovered_by": "ast_python_argparse",
					}, sym, "argparse.ArgumentParser")
				}
			}
		case "java", "kotlin":
			for _, sym := range fa.Symbols {
				if !isSpringBootMain(fa, sym) {
					continue
				}
				name := firstNonEmpty(sym.Qualified, sym.Name, filepath.ToSlash(sym.File))
				appendCLI(&out, seen, path, name, "AST-derived Spring Boot service launcher", []string{"deterministic", "spring-boot", fa.Language}, map[string]any{
					"command":       "java -jar application.jar",
					"handler":       sym.Qualified,
					"entry_method":  sym.Qualified,
					"runtime":       "jvm",
					"platform":      "process",
					"discovered_by": "ast_spring_boot_main",
				}, sym, "SpringApplication.run")
			}
		}
	}
	return out
}

var cobraCommandRE = regexp.MustCompile(`(?s)(?:var\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*)?&cobra\.Command\s*\{.*?Use:\s*"([^"]+)".*?(?:RunE?|PreRunE?|PostRunE?):\s*([A-Za-z_][A-Za-z0-9_]*)`)

func appendCobraCommands(idx *astpkg.ProjectIndex, out *[]candidate, seen map[string]struct{}, path string, fa *astpkg.FileAST) {
	if idx == nil || fa == nil || !fileImports(fa, "github.com/spf13/cobra") {
		return
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, path))
	if err != nil {
		return
	}
	src := string(b)
	for _, m := range cobraCommandRE.FindAllStringSubmatchIndex(src, -1) {
		if len(m) < 8 {
			continue
		}
		use := src[m[4]:m[5]]
		handler := src[m[6]:m[7]]
		varName := ""
		if m[2] >= 0 {
			varName = src[m[2]:m[3]]
		}
		line := 1 + strings.Count(src[:m[0]], "\n")
		name := strings.Fields(use)
		cmdName := use
		if len(name) > 0 {
			cmdName = name[0]
		}
		key := strings.ToLower(path + "|" + cmdName)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		loc := candidateLocation{File: path, StartLine: line, EndLine: line}
		*out = append(*out, candidate{
			Type:       "cli_command",
			Name:       cmdName,
			Summary:    "AST-derived Go Cobra command entrypoint",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "cobra", "go"},
			Details: map[string]any{
				"command":       cmdName,
				"binary":        cmdName,
				"handler":       handler,
				"entry_method":  handler,
				"variable":      varName,
				"runtime":       "go",
				"platform":      "process",
				"discovered_by": "ast_go_cobra_command",
			},
			Locations: []candidateLocation{loc},
			Evidence: []candidateEvidence{{
				File:      loc.File,
				StartLine: loc.StartLine,
				EndLine:   loc.EndLine,
				Snippet:   `cobra.Command{Use: "` + use + `"}`,
				Source:    "deterministic_ast",
			}},
		})
	}
}

func appendCLI(out *[]candidate, seen map[string]struct{}, path, name, summary string, tags []string, details map[string]any, sym astpkg.SymbolDef, snippet string) {
	key := strings.ToLower(path + "|" + name)
	if _, dup := seen[key]; dup {
		return
	}
	seen[key] = struct{}{}
	loc := candidateLocation{
		File:      sym.File,
		StartLine: int(sym.Range.StartLine) + 1,
		EndLine:   int(sym.Range.EndLine) + 1,
	}
	*out = append(*out, candidate{
		Type:       "cli_command",
		Name:       name,
		Summary:    summary,
		Confidence: 1.0,
		Tags:       tags,
		Details:    details,
		Locations:  []candidateLocation{loc},
		Evidence: []candidateEvidence{{
			File:      loc.File,
			StartLine: loc.StartLine,
			EndLine:   loc.EndLine,
			Snippet:   snippet,
			Source:    "deterministic_ast",
		}},
	})
}

func isPythonLambdaHandlerSymbol(idx *astpkg.ProjectIndex, fa *astpkg.FileAST, sym astpkg.SymbolDef) bool {
	if sym.Kind != astpkg.SymbolKindFunction {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(sym.Name))
	if name != "handler" && name != "lambda_handler" {
		return false
	}
	if strings.Contains(sym.Qualified, ".") {
		return false
	}
	return fileImportsAWSLambda(fa) || configReferencesPythonHandler(idx, sym)
}

func fileImportsAWSLambda(fa *astpkg.FileAST) bool {
	if fa == nil {
		return false
	}
	for _, imp := range fa.Imports {
		p := strings.ToLower(strings.TrimSpace(imp.Path))
		if strings.Contains(p, "aws_lambda") || strings.Contains(p, "awslambda") {
			return true
		}
	}
	return false
}

func configReferencesPythonHandler(idx *astpkg.ProjectIndex, sym astpkg.SymbolDef) bool {
	if idx == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(sym.Name))
	moduleName := strings.ToLower(pythonHandlerName(sym))
	for _, cf := range idx.Configs {
		for _, ent := range cf.Entries {
			key := strings.ToLower(strings.TrimSpace(ent.Key))
			if !strings.Contains(key, "handler") {
				continue
			}
			value := strings.ToLower(strings.Trim(strings.TrimSpace(ent.Value), `"'`))
			switch {
			case value == moduleName:
				return true
			case strings.HasSuffix(value, "."+name):
				return true
			}
		}
	}
	return false
}

func isPythonArgparseEntrypoint(fa *astpkg.FileAST, sym astpkg.SymbolDef) bool {
	if sym.Kind != astpkg.SymbolKindFunction {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(sym.Name))
	if name != "main" && name != "run" {
		return false
	}
	if strings.Contains(sym.Qualified, ".") {
		return false
	}
	if !fileImports(fa, "argparse") && !fileCalls(fa, sym.Qualified, "argparse", "ArgumentParser") {
		return false
	}
	return true
}

func isSpringBootMain(fa *astpkg.FileAST, sym astpkg.SymbolDef) bool {
	if sym.Kind != astpkg.SymbolKindMethod && sym.Kind != astpkg.SymbolKindFunction {
		return false
	}
	if strings.TrimSpace(sym.Name) != "main" {
		return false
	}
	return fileCalls(fa, sym.Qualified, "SpringApplication", "run")
}

func fileImports(fa *astpkg.FileAST, module string) bool {
	if fa == nil {
		return false
	}
	module = strings.ToLower(module)
	for _, imp := range fa.Imports {
		if strings.EqualFold(imp.Path, module) || strings.HasPrefix(strings.ToLower(imp.Path), module+".") {
			return true
		}
	}
	return false
}

func fileCalls(fa *astpkg.FileAST, caller, receiverNeedle, calleeNeedle string) bool {
	if fa == nil {
		return false
	}
	receiverNeedle = strings.ToLower(receiverNeedle)
	calleeNeedle = strings.ToLower(calleeNeedle)
	for _, call := range fa.Calls {
		if caller != "" && call.Caller != caller {
			continue
		}
		receiver := strings.ToLower(call.ReceiverRaw)
		callee := strings.ToLower(call.CalleeRaw)
		raw := strings.ToLower(call.CalleeRaw)
		if strings.Contains(receiver, receiverNeedle) && strings.Contains(callee, calleeNeedle) {
			return true
		}
		if strings.Contains(raw, strings.ToLower(receiverNeedle+"."+calleeNeedle)) {
			return true
		}
	}
	return false
}

func pythonHandlerName(sym astpkg.SymbolDef) string {
	file := strings.TrimSpace(sym.File)
	base := strings.TrimSuffix(filepath.ToSlash(file), ".py")
	base = strings.TrimSuffix(base, "/__init__")
	base = strings.ReplaceAll(base, "/", ".")
	if base == "" {
		return sym.Name
	}
	return base + "." + sym.Name
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
