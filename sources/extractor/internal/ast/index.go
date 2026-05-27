package ast

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Build walks repoRoot, parses every source and config file using tree-sitter,
// and returns a fully-resolved ProjectIndex.
//
// Language is the primary language hint (from langdetect). Mixed-language
// projects are handled automatically by extension detection.
//
// The analysis runs with workers goroutines for parsing, then single-threaded
// resolution passes. Progress is reported via progressFn (which receives the
// count of files processed so far).
func Build(ctx context.Context, repoRoot, primaryLanguage string, workers int, progressFn func(done, total int)) (*ProjectIndex, error) {
	if workers <= 0 {
		workers = 8
	}

	// ── Step 1: walk the repo and collect file paths ──────────────────────
	var sourceFiles []string
	var configFiles []string

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			name := d.Name()
			if isSkippedDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(rel))

		if LanguageForExtension(ext) != "" {
			sourceFiles = append(sourceFiles, rel)
		} else if ConfigFormatForExtension(ext) != "" {
			configFiles = append(configFiles, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	total := len(sourceFiles) + len(configFiles)
	var mu sync.Mutex
	done := 0
	reportProgress := func() {
		mu.Lock()
		done++
		if progressFn != nil {
			progressFn(done, total)
		}
		mu.Unlock()
	}

	// ── Step 2: parse source files in parallel ────────────────────────────
	idx := &ProjectIndex{
		Files:      make(map[string]*FileAST),
		Symbols:    make(map[string][]SymbolDef),
		CallGraph:  make(map[string][]CallSite),
		TypeMap:    make(map[string][]string),
		FieldTypes: make(map[string]string),
		Configs:    make(map[string]*ConfigFile),
		Language:   primaryLanguage,
	}

	type parseResult struct {
		path string
		fa   *FileAST
		err  error
	}
	resultCh := make(chan parseResult, len(sourceFiles))

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, rel := range sourceFiles {
		rel := rel
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fa, err := ParseFile(ctx, repoRoot, rel)
			resultCh <- parseResult{path: rel, fa: fa, err: err}
			reportProgress()
		}()
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for r := range resultCh {
		if r.err != nil {
			util.Warn("ast.index", "parse error", map[string]any{"file": r.path, "error": r.err.Error()})
			continue
		}
		if r.fa == nil {
			continue
		}
		mu.Lock()
		idx.Files[r.path] = r.fa
		mu.Unlock()
	}

	// ── Step 3: parse config files (sequential, cheap) ────────────────────
	for _, rel := range configFiles {
		cf, err := ParseConfigFile(repoRoot, rel)
		if err != nil {
			util.Warn("ast.index", "config parse error", map[string]any{"file": rel, "error": err.Error()})
			continue
		}
		if cf != nil {
			idx.Configs[rel] = cf
		}
		reportProgress()
	}

	// ── Step 4: build global symbol table ─────────────────────────────────
	for _, fa := range idx.Files {
		for _, sym := range fa.Symbols {
			idx.Symbols[sym.Qualified] = append(idx.Symbols[sym.Qualified], sym)
		}
	}

	// ── Step 5: build call graph ──────────────────────────────────────────
	for _, fa := range idx.Files {
		for _, call := range fa.Calls {
			if call.Caller != "" {
				idx.CallGraph[call.Caller] = append(idx.CallGraph[call.Caller], call)
			}
		}
	}

	// ── Step 6: cross-file symbol resolution ─────────────────────────────
	resolveCallees(idx)

	// ── Step 7: build type map (interface → implementations) ──────────────
	buildTypeMap(idx)

	// ── Step 8: detect framework bindings ────────────────────────────────
	idx.Frameworks = detectFrameworks(idx)

	util.Info("ast.index", "project index built", map[string]any{
		"files":     len(idx.Files),
		"symbols":   len(idx.Symbols),
		"callgraph": len(idx.CallGraph),
		"configs":   len(idx.Configs),
		"frameworks": len(idx.Frameworks),
	})

	return idx, nil
}

// resolveCallees attempts to resolve every CallSite.CalleeRaw to a qualified
// symbol. It uses:
//  1. Exact qualified name lookup in the symbol table.
//  2. File-local imports to expand partial names.
//  3. Receiver type inference for method calls.
func resolveCallees(idx *ProjectIndex) {
	for _, fa := range idx.Files {
		// Build a local import alias → resolved qualified prefix map.
		importMap := buildImportMap(fa, idx)

		for i := range fa.Calls {
			call := &fa.Calls[i]
			resolved := resolveCallee(call.CalleeRaw, importMap, idx, fa)
			call.CalleeResolved = resolved
			// Update the call graph entry.
			if call.Caller != "" {
				entries := idx.CallGraph[call.Caller]
				for j := range entries {
					if entries[j].File == call.File &&
						entries[j].Range.StartByte == call.Range.StartByte {
						idx.CallGraph[call.Caller][j].CalleeResolved = resolved
					}
				}
			}
		}
	}
}

// buildImportMap builds a map from local alias → qualified prefix for one file.
func buildImportMap(fa *FileAST, idx *ProjectIndex) map[string]string {
	m := make(map[string]string, len(fa.Imports))
	for _, imp := range fa.Imports {
		if imp.Alias != "" {
			m[imp.Alias] = imp.Path
		}
	}
	return m
}

// resolveCallee attempts to resolve a raw callee string to qualified symbols.
func resolveCallee(raw string, importMap map[string]string, idx *ProjectIndex, fa *FileAST) []string {
	// 1. Exact match in the symbol table.
	if syms, ok := idx.Symbols[raw]; ok {
		out := make([]string, 0, len(syms))
		for _, s := range syms {
			out = append(out, s.Qualified)
		}
		return unique(out)
	}

	// 2. Strip receiver prefix: "repo.findById" → try "findById" in all
	//    types and try the receiver type from the import map.
	parts := splitQualified(raw)
	if len(parts) == 2 {
		receiver, method := parts[0], parts[1]

		// Try to find the receiver's type from field declarations.
		// fieldKey: caller type + "." + field name.
		// We look for any symbol with the method name on any type that
		// imports from the same package as the receiver alias.
		prefix, hasPrefix := importMap[receiver]
		if hasPrefix {
			// Look for types defined in the imported package.
			candidates := symbolsWithMethod(idx, method, prefix)
			if len(candidates) > 0 {
				return candidates
			}
		}

		// Fall back: return any symbol with this method name.
		candidates := symbolsWithMethod(idx, method, "")
		if len(candidates) > 0 {
			return candidates
		}
	}

	// 3. Simple method name match (last segment).
	if len(parts) >= 1 {
		method := parts[len(parts)-1]
		candidates := symbolsWithMethod(idx, method, "")
		if len(candidates) > 0 {
			return candidates
		}
	}

	// Unresolved: return the raw name so the walker can still reference it.
	return []string{raw}
}

// symbolsWithMethod returns all qualified symbols whose unqualified name
// equals method and (optionally) whose qualified name contains prefix.
func symbolsWithMethod(idx *ProjectIndex, method, prefix string) []string {
	var out []string
	for qualified, defs := range idx.Symbols {
		for _, def := range defs {
			if def.Name == method || def.Qualified == method {
				if prefix == "" || strings.Contains(def.File, strings.ReplaceAll(prefix, ".", "/")) {
					out = append(out, qualified)
					break
				}
			}
		}
	}
	return unique(out)
}

// splitQualified splits "Foo.bar" or "foo.bar.baz" into parts.
func splitQualified(s string) []string {
	// Handle method chains by splitting on the last dot.
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		return []string{s[:idx], s[idx+1:]}
	}
	return []string{s}
}

// buildTypeMap builds the interface → implementations map by looking for
// symbols that implement interfaces (heuristic: matching method signatures).
func buildTypeMap(idx *ProjectIndex) {
	// Simple heuristic: two types that share method names with the same
	// unqualified name are potentially related.
	// A more accurate approach requires per-language type analysis.
	// For now we just collect type hierarchies from annotations.
	for _, fa := range idx.Files {
		for _, sym := range fa.Symbols {
			if sym.Kind == SymbolKindClass || sym.Kind == SymbolKindInterface {
				for _, ann := range sym.Annotations {
					// Java @Component, @Service, @Repository, etc. — all are
					// concrete implementations of a Spring bean.
					if isImplementationAnnotation(ann.Name) {
						idx.TypeMap["__spring_bean__"] = append(
							idx.TypeMap["__spring_bean__"], sym.Qualified)
					}
				}
			}
		}
	}
}

func isImplementationAnnotation(name string) bool {
	switch name {
	case "Component", "Service", "Repository", "Controller", "RestController",
		"Injectable", "Provider":
		return true
	}
	return false
}

// FrameworkDetector is the interface that framework-specific detectors implement.
// Detectors are registered via RegisterFrameworkDetector and run during Build().
type FrameworkDetector interface {
	Name() string
	Detect(idx *ProjectIndex) []FrameworkBinding
}

// registeredDetectors is the global list of framework detectors populated by
// RegisterFrameworkDetector (called from framework/*.go init() functions via
// the ast/framework package).
var registeredDetectors []FrameworkDetector

// RegisterFrameworkDetector adds a detector to the global registry.
// Called from init() functions in the framework sub-packages.
func RegisterFrameworkDetector(d FrameworkDetector) {
	registeredDetectors = append(registeredDetectors, d)
}

// detectFrameworks runs all framework detectors and returns the bindings.
func detectFrameworks(idx *ProjectIndex) []FrameworkBinding {
	var all []FrameworkBinding
	for _, detector := range registeredDetectors {
		all = append(all, detector.Detect(idx)...)
	}
	return all
}

// isSkippedDir reports whether a directory should be skipped when walking.
func isSkippedDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode", ".gradle", ".mvn",
		"node_modules", "bower_components", "vendor", ".bundle",
		"__pycache__", ".venv", "venv", ".tox", ".pytest_cache",
		"target", "build", "out", "bin", "dist", "tmp", ".cache",
		".m2", ".ivy2", ".cargo/registry", "testdata", "fixtures",
		".terraform", ".serverless":
		return true
	}
	return false
}

// unique deduplicates a string slice preserving order.
func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
