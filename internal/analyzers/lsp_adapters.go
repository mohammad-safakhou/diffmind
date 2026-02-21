package analyzers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func probeGoplsLSP(path string, sourceRoot string, timeout time.Duration) (bool, string) {
	return probeGenericLSP(path, []string{"serve"}, sourceRoot, timeout)
}

func probePyrightLSP(path string, sourceRoot string, timeout time.Duration) (bool, string) {
	return probeGenericLSP(path, []string{"--stdio"}, sourceRoot, timeout)
}

func probeTsserverLSP(path string, sourceRoot string, timeout time.Duration) (bool, string) {
	return probeGenericLSP(path, []string{"--stdio"}, sourceRoot, timeout)
}

func probeGenericLSP(path string, args []string, sourceRoot string, timeout time.Duration) (bool, string) {
	if strings.TrimSpace(path) == "" {
		return false, "empty tool path"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := startLSPClient(ctx, path, args, sourceRoot)
	if err != nil {
		return false, err.Error()
	}
	defer client.Close()

	if err := client.Initialize(ctx, sourceRoot); err != nil {
		return false, err.Error()
	}
	_ = client.Notify("exit", map[string]any{})
	return true, "lsp probe ok"
}

func runGoplsSemanticExtraction(sourceRoot string, toolPath string) (string, adapterSemanticDocument, error) {
	timeout := parsePositiveEnvInt("DIFFMIND_GOPLS_TIMEOUT_SECONDS", 60)
	maxFiles := parsePositiveEnvInt("DIFFMIND_GOPLS_MAX_FILES", 5000)
	includeTests := strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_GOPLS_INCLUDE_TESTS")), "true")
	return runLSPDocumentSymbolExtraction(
		sourceRoot,
		toolPath,
		[]string{"serve"},
		[]string{".go"},
		"go-lsp",
		"go",
		timeout,
		maxFiles,
		includeTests,
	)
}

func runPyrightSemanticExtraction(sourceRoot string, toolPath string) (string, adapterSemanticDocument, error) {
	timeout := parsePositiveEnvInt("DIFFMIND_PYRIGHT_TIMEOUT_SECONDS", 60)
	maxFiles := parsePositiveEnvInt("DIFFMIND_PYRIGHT_MAX_FILES", 5000)
	includeTests := strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_PYRIGHT_INCLUDE_TESTS")), "true")
	return runLSPDocumentSymbolExtraction(
		sourceRoot,
		toolPath,
		[]string{"--stdio"},
		[]string{".py"},
		"python-lsp",
		"python",
		timeout,
		maxFiles,
		includeTests,
	)
}

func runTsserverSemanticExtraction(sourceRoot string, toolPath string) (string, adapterSemanticDocument, error) {
	timeout := parsePositiveEnvInt("DIFFMIND_TSSERVER_TIMEOUT_SECONDS", 60)
	maxFiles := parsePositiveEnvInt("DIFFMIND_TSSERVER_MAX_FILES", 5000)
	includeTests := strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_TSSERVER_INCLUDE_TESTS")), "true")
	return runLSPDocumentSymbolExtraction(
		sourceRoot,
		toolPath,
		[]string{"--stdio"},
		[]string{".ts", ".tsx", ".js", ".jsx"},
		"typescript-lsp",
		"typescript",
		timeout,
		maxFiles,
		includeTests,
	)
}

func runLSPDocumentSymbolExtraction(
	sourceRoot string,
	toolPath string,
	toolArgs []string,
	extensions []string,
	packageName string,
	defaultLanguageID string,
	timeoutSeconds int,
	maxFiles int,
	includeTests bool,
) (string, adapterSemanticDocument, error) {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if maxFiles <= 0 {
		maxFiles = 5000
	}
	files, err := collectSourceFilesByExtensions(sourceRoot, extensions, maxFiles, includeTests)
	if err != nil {
		return "", adapterSemanticDocument{}, err
	}
	if len(files) == 0 {
		return "lsp: no matching source files", adapterSemanticDocument{}, nil
	}
	if strings.TrimSpace(toolPath) == "" {
		return "", adapterSemanticDocument{}, fmt.Errorf("empty tool path")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := startLSPClient(ctx, toolPath, toolArgs, sourceRoot)
	if err != nil {
		return "", adapterSemanticDocument{}, err
	}
	defer client.Close()

	if err := client.Initialize(ctx, sourceRoot); err != nil {
		return "", adapterSemanticDocument{}, err
	}

	pkg := adapterSemanticPackage{
		Name:  packageName,
		Files: make([]adapterSemanticFile, 0, len(files)),
	}
	symbolCount := 0
	requestErrors := 0
	linesByPath := make(map[string][]string, len(files))

	for _, abs := range files {
		content, err := os.ReadFile(abs)
		if err != nil {
			requestErrors++
			continue
		}
		rel, err := filepath.Rel(sourceRoot, abs)
		if err != nil {
			rel = abs
		}
		rel = filepath.ToSlash(rel)
		uri := pathToFileURI(abs)
		languageID := languageIDForPath(rel, defaultLanguageID)
		linesByPath[rel] = strings.Split(string(content), "\n")

		_ = client.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageID,
				"version":    1,
				"text":       string(content),
			},
		})
		raw, err := client.Request(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}, 6*time.Second)
		if err != nil {
			requestErrors++
			_ = client.Notify("textDocument/didClose", map[string]any{
				"textDocument": map[string]any{"uri": uri},
			})
			continue
		}
		symbols := parseJDTLSDocumentSymbols(raw, rel, content)
		if len(symbols) > 0 {
			symbolCount += len(symbols)
			pkg.Files = append(pkg.Files, adapterSemanticFile{
				Path:    rel,
				Symbols: symbols,
			})
		}
		_ = client.Notify("textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
	}

	xrefCalls, xrefErrors := enrichPackageWithLSPReferences(ctx, client, sourceRoot, &pkg, linesByPath)

	_ = client.Shutdown(ctx)
	if len(pkg.Files) == 0 {
		return fmt.Sprintf("lsp: files=0 symbols=0 request_errors=%d xref_calls=%d xref_errors=%d include_tests=%t", requestErrors, xrefCalls, xrefErrors, includeTests), adapterSemanticDocument{}, nil
	}
	return fmt.Sprintf("lsp: files=%d symbols=%d xref_calls=%d request_errors=%d xref_errors=%d include_tests=%t", len(pkg.Files), symbolCount, xrefCalls, requestErrors, xrefErrors, includeTests),
		adapterSemanticDocument{Packages: []adapterSemanticPackage{pkg}},
		nil
}

type lspLocationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange lspRange `json:"targetRange"`
}

type symbolCandidate struct {
	File string
	Sym  adapterSemanticSymbol
}

type lspCallSite struct {
	Line    int
	Col     int
	Token   string
	Snippet string
}

var lspCallTokenRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

var lspCallSkipKeywords = map[string]struct{}{
	"if": {}, "for": {}, "while": {}, "switch": {}, "catch": {}, "new": {},
	"return": {}, "throw": {}, "case": {}, "typeof": {}, "sizeof": {},
}

func enrichPackageWithLSPReferences(
	ctx context.Context,
	client *lspClient,
	sourceRoot string,
	pkg *adapterSemanticPackage,
	linesByPath map[string][]string,
) (int, int) {
	if pkg == nil || len(pkg.Files) == 0 || strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_LSP_ENABLE_XREF")), "false") {
		return 0, 0
	}
	maxSymbols := parsePositiveEnvInt("DIFFMIND_LSP_MAX_REFERENCE_SYMBOLS", 120)
	maxRefsPerSymbol := parsePositiveEnvInt("DIFFMIND_LSP_MAX_REFERENCES_PER_SYMBOL", 25)
	minXrefCalls := parsePositiveEnvInt("DIFFMIND_LSP_MIN_XREF_CALLS", 10)
	maxDefinitionQueries := parsePositiveEnvInt("DIFFMIND_LSP_MAX_DEFINITION_QUERIES", 600)
	maxDefinitionSitesPerFile := parsePositiveEnvInt("DIFFMIND_LSP_MAX_DEFINITION_SITES_PER_FILE", 80)
	enableDefinitionFallback := !strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_LSP_ENABLE_DEFINITION_FALLBACK")), "false")
	if maxSymbols <= 0 || maxRefsPerSymbol <= 0 {
		return 0, 0
	}

	byFile := map[string][]adapterSemanticSymbol{}
	fileIndexByPath := map[string]int{}
	candidates := make([]symbolCandidate, 0, 256)
	for i := range pkg.Files {
		p := strings.TrimSpace(pkg.Files[i].Path)
		if p == "" {
			continue
		}
		fileIndexByPath[p] = i
		for _, sym := range pkg.Files[i].Symbols {
			if !isCallableSymbolKind(sym.Kind) || sym.Line < 1 {
				continue
			}
			byFile[p] = append(byFile[p], sym)
			candidates = append(candidates, symbolCandidate{File: p, Sym: sym})
		}
	}
	for p := range byFile {
		sort.Slice(byFile[p], func(i, j int) bool {
			if byFile[p][i].Line == byFile[p][j].Line {
				return byFile[p][i].Col < byFile[p][j].Col
			}
			return byFile[p][i].Line < byFile[p][j].Line
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].File == candidates[j].File {
			if candidates[i].Sym.Line == candidates[j].Sym.Line {
				return candidates[i].Sym.Col < candidates[j].Sym.Col
			}
			return candidates[i].Sym.Line < candidates[j].Sym.Line
		}
		return candidates[i].File < candidates[j].File
	})
	if len(candidates) > maxSymbols {
		candidates = candidates[:maxSymbols]
	}

	addedCalls := 0
	requestErrors := 0
	seen := map[string]struct{}{}
	referencesUnsupported := false

	for _, candidate := range candidates {
		if referencesUnsupported {
			break
		}
		abs := filepath.Join(sourceRoot, filepath.FromSlash(candidate.File))
		raw, err := client.Request(ctx, "textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": pathToFileURI(abs)},
			"position": map[string]any{
				"line":      max(0, candidate.Sym.Line-1),
				"character": max(0, candidate.Sym.Col-1),
			},
			"context": map[string]any{
				"includeDeclaration": false,
			},
		}, 4*time.Second)
		if err != nil {
			requestErrors++
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(msg, "method not found") || strings.Contains(msg, "does not support") {
				referencesUnsupported = true
			}
			continue
		}

		refs := parseLSPLocations(raw)
		if len(refs) > maxRefsPerSymbol {
			refs = refs[:maxRefsPerSymbol]
		}
		for _, ref := range refs {
			refAbs := fileURIToPath(ref.URI)
			if strings.TrimSpace(refAbs) == "" {
				continue
			}
			rel, err := filepath.Rel(sourceRoot, refAbs)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			refLine := ref.Range.Start.Line + 1
			refCol := ref.Range.Start.Character + 1
			if rel == candidate.File && refLine == candidate.Sym.Line && refCol == candidate.Sym.Col {
				continue
			}

			caller, ok := nearestCallableSymbol(byFile[rel], refLine)
			if !ok {
				continue
			}
			callerName := strings.TrimSpace(caller.Name)
			calleeName := strings.TrimSpace(candidate.Sym.Name)
			if callerName == "" || calleeName == "" {
				continue
			}
			if callerName == calleeName && rel == candidate.File {
				continue
			}
			dedup := strings.Join([]string{rel, callerName, candidate.File, calleeName, strconv.Itoa(refLine), strconv.Itoa(refCol)}, "|")
			if _, ok := seen[dedup]; ok {
				continue
			}
			seen[dedup] = struct{}{}

			call := adapterSemanticCall{
				Caller:  callerName,
				Callee:  calleeName,
				Kind:    "lsp_reference",
				File:    rel,
				Line:    refLine,
				Col:     refCol,
				Snippet: lspLineSnippet(linesByPath[rel], refLine),
			}
			idx, ok := fileIndexByPath[rel]
			if !ok {
				pkg.Files = append(pkg.Files, adapterSemanticFile{Path: rel})
				idx = len(pkg.Files) - 1
				fileIndexByPath[rel] = idx
			}
			pkg.Files[idx].Calls = append(pkg.Files[idx].Calls, call)
			addedCalls++
		}
	}

	if enableDefinitionFallback && maxDefinitionQueries > 0 && (referencesUnsupported || addedCalls < minXrefCalls) {
		defCalls, defErrors := enrichPackageWithLSPDefinitions(
			ctx,
			client,
			sourceRoot,
			pkg,
			linesByPath,
			byFile,
			fileIndexByPath,
			seen,
			maxDefinitionQueries,
			maxDefinitionSitesPerFile,
		)
		addedCalls += defCalls
		requestErrors += defErrors
	}

	return addedCalls, requestErrors
}

func enrichPackageWithLSPDefinitions(
	ctx context.Context,
	client *lspClient,
	sourceRoot string,
	pkg *adapterSemanticPackage,
	linesByPath map[string][]string,
	byFile map[string][]adapterSemanticSymbol,
	fileIndexByPath map[string]int,
	seen map[string]struct{},
	maxQueries int,
	maxSitesPerFile int,
) (int, int) {
	if pkg == nil || client == nil || len(byFile) == 0 || maxQueries <= 0 || maxSitesPerFile <= 0 {
		return 0, 0
	}
	files := sortedSymbolFiles(byFile)
	if len(files) == 0 {
		return 0, 0
	}

	added := 0
	errors := 0
	queries := 0

	for _, filePath := range files {
		if queries >= maxQueries {
			break
		}
		lines := linesByPath[filePath]
		if len(lines) == 0 {
			continue
		}
		sites := extractDefinitionCallSites(lines, maxSitesPerFile)
		if len(sites) == 0 {
			continue
		}
		for _, site := range sites {
			if queries >= maxQueries {
				break
			}
			caller, ok := nearestCallableSymbol(byFile[filePath], site.Line)
			if !ok {
				continue
			}
			callerName := strings.TrimSpace(caller.Name)
			if callerName == "" {
				continue
			}

			raw, err := client.Request(ctx, "textDocument/definition", map[string]any{
				"textDocument": map[string]any{
					"uri": pathToFileURI(filepath.Join(sourceRoot, filepath.FromSlash(filePath))),
				},
				"position": map[string]any{
					"line":      max(0, site.Line-1),
					"character": max(0, site.Col-1),
				},
			}, 4*time.Second)
			queries++
			if err != nil {
				errors++
				continue
			}
			defs := parseLSPDefinitionLocations(raw)
			if len(defs) == 0 {
				continue
			}

			for _, def := range defs {
				defAbs := fileURIToPath(def.URI)
				if strings.TrimSpace(defAbs) == "" {
					continue
				}
				defRel, err := filepath.Rel(sourceRoot, defAbs)
				if err != nil {
					continue
				}
				defRel = filepath.ToSlash(defRel)
				callee, ok := nearestCallableSymbol(byFile[defRel], def.Range.Start.Line+1)
				if !ok {
					continue
				}
				calleeName := strings.TrimSpace(callee.Name)
				if calleeName == "" {
					continue
				}
				if callerName == calleeName && filePath == defRel {
					continue
				}
				dedup := strings.Join([]string{
					filePath, callerName, defRel, calleeName,
					strconv.Itoa(site.Line), strconv.Itoa(site.Col), "lsp_definition",
				}, "|")
				if _, ok := seen[dedup]; ok {
					continue
				}
				seen[dedup] = struct{}{}

				call := adapterSemanticCall{
					Caller:  callerName,
					Callee:  calleeName,
					Kind:    "lsp_definition",
					File:    filePath,
					Line:    site.Line,
					Col:     site.Col,
					Snippet: site.Snippet,
				}
				idx, ok := fileIndexByPath[filePath]
				if !ok {
					pkg.Files = append(pkg.Files, adapterSemanticFile{Path: filePath})
					idx = len(pkg.Files) - 1
					fileIndexByPath[filePath] = idx
				}
				pkg.Files[idx].Calls = append(pkg.Files[idx].Calls, call)
				added++
				break
			}
		}
	}
	return added, errors
}

func parseLSPLocations(raw json.RawMessage) []lspLocation {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var direct []lspLocation
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		out := make([]lspLocation, 0, len(links))
		for _, item := range links {
			out = append(out, lspLocation{
				URI:   item.TargetURI,
				Range: item.TargetRange,
			})
		}
		return out
	}
	return nil
}

func parseLSPDefinitionLocations(raw json.RawMessage) []lspLocation {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single.URI) != "" {
		return []lspLocation{single}
	}
	var direct []lspLocation
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var singleLink lspLocationLink
	if err := json.Unmarshal(raw, &singleLink); err == nil && strings.TrimSpace(singleLink.TargetURI) != "" {
		return []lspLocation{{URI: singleLink.TargetURI, Range: singleLink.TargetRange}}
	}
	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		out := make([]lspLocation, 0, len(links))
		for _, item := range links {
			if strings.TrimSpace(item.TargetURI) == "" {
				continue
			}
			out = append(out, lspLocation{
				URI:   item.TargetURI,
				Range: item.TargetRange,
			})
		}
		return out
	}
	return nil
}

func nearestCallableSymbol(symbols []adapterSemanticSymbol, line int) (adapterSemanticSymbol, bool) {
	if len(symbols) == 0 || line < 1 {
		return adapterSemanticSymbol{}, false
	}
	idx := -1
	for i := range symbols {
		if symbols[i].Line <= line && isCallableSymbolKind(symbols[i].Kind) {
			idx = i
			continue
		}
		if symbols[i].Line > line {
			break
		}
	}
	if idx < 0 {
		return adapterSemanticSymbol{}, false
	}
	return symbols[idx], true
}

func isCallableSymbolKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "function", "method", "constructor":
		return true
	default:
		return false
	}
}

func lspLineSnippet(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return "lsp reference"
	}
	return strings.TrimSpace(lines[line-1])
}

func extractDefinitionCallSites(lines []string, maxSites int) []lspCallSite {
	if maxSites <= 0 || len(lines) == 0 {
		return nil
	}
	out := make([]lspCallSite, 0, min(maxSites, 64))
	for i, raw := range lines {
		if len(out) >= maxSites {
			break
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") {
			continue
		}
		matches := lspCallTokenRe.FindAllStringSubmatchIndex(raw, -1)
		for _, m := range matches {
			if len(m) < 4 {
				continue
			}
			token := strings.TrimSpace(raw[m[2]:m[3]])
			if token == "" {
				continue
			}
			if _, skip := lspCallSkipKeywords[strings.ToLower(token)]; skip {
				continue
			}
			out = append(out, lspCallSite{
				Line:    i + 1,
				Col:     m[2] + 1,
				Token:   token,
				Snippet: strings.TrimSpace(raw),
			})
			if len(out) >= maxSites {
				break
			}
		}
	}
	return out
}

func sortedSymbolFiles(byFile map[string][]adapterSemanticSymbol) []string {
	out := make([]string, 0, len(byFile))
	for k := range byFile {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fileURIToPath(uri string) string {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return ""
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return ""
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		p = u.Path
	}
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func collectSourceFilesByExtensions(root string, extensions []string, maxFiles int, includeTests bool) ([]string, error) {
	if maxFiles <= 0 {
		maxFiles = 5000
	}
	extSet := map[string]struct{}{}
	for _, ext := range extensions {
		e := strings.ToLower(strings.TrimSpace(ext))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = struct{}{}
	}
	if len(extSet) == 0 {
		return nil, fmt.Errorf("no extensions configured for lsp collection")
	}

	out := make([]string, 0, maxFiles)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".diffmind", ".idea", ".settings", ".metadata", ".jdtls", ".gradle", "node_modules", "vendor", "target", "build", "out", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := extSet[ext]; !ok {
			return nil
		}
		if !includeTests {
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && isTestSourcePath(filepath.ToSlash(rel)) {
				return nil
			}
		}
		out = append(out, path)
		if len(out) >= maxFiles {
			return io.EOF
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func startLSPClient(ctx context.Context, toolPath string, args []string, sourceRoot string) (*lspClient, error) {
	if strings.TrimSpace(toolPath) == "" {
		return nil, fmt.Errorf("empty lsp tool path")
	}
	cmd := exec.CommandContext(ctx, toolPath, args...)
	cmd.Dir = sourceRoot

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start lsp tool: %w", err)
	}

	c := &lspClient{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		readDone: make(chan error, 1),
		pending:  map[string]chan lspResponse{},
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			c.stderrMu.Lock()
			c.stderr.WriteString(sc.Text())
			c.stderr.WriteByte('\n')
			c.stderrMu.Unlock()
		}
	}()
	go c.readLoop()
	return c, nil
}

func parsePositiveEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func languageIDForPath(path string, fallback string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".go":
		return "go"
	default:
		return fallback
	}
}
