package analyzers

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var javaImportLineRe = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.*]+)\s*;`)

type lspRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *lspRPCError    `json:"error,omitempty"`
}

type lspClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	readDone chan error

	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan lspResponse
	nextID    int64

	stderrMu sync.Mutex
	stderr   strings.Builder
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspDocumentSymbol struct {
	Name     string              `json:"name"`
	Kind     int                 `json:"kind"`
	Range    lspRange            `json:"range"`
	Children []lspDocumentSymbol `json:"children"`
}

type lspSymbolInformation struct {
	Name     string      `json:"name"`
	Kind     int         `json:"kind"`
	Location lspLocation `json:"location"`
}

func probeJDTLSLSP(path string, sourceRoot string, timeout time.Duration) (bool, string) {
	if strings.TrimSpace(path) == "" {
		return false, "empty jdtls path"
	}
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	workspaceDir := filepath.Join(os.TempDir(), "diffmind-jdtls-probe-"+jdtlsStableID(sourceRoot))
	client, err := startJDTLSClient(ctx, path, sourceRoot, workspaceDir)
	if err != nil {
		return false, err.Error()
	}
	defer client.Close()

	if err := client.Initialize(ctx, sourceRoot); err != nil {
		return false, err.Error()
	}
	// Some jdtls distributions don't reliably answer shutdown during short probes.
	// A successful initialize is enough to confirm LSP viability.
	_ = client.Notify("exit", map[string]any{})
	return true, "lsp probe ok"
}

func runJDTLSSemanticExtraction(sourceRoot string, toolPath string) (string, adapterSemanticDocument, error) {
	timeout := 90 * time.Second
	if raw := strings.TrimSpace(os.Getenv("DIFFMIND_JDTLS_TIMEOUT_SECONDS")); raw != "" {
		if sec, err := strconv.Atoi(raw); err == nil && sec > 0 {
			timeout = time.Duration(sec) * time.Second
		}
	}

	maxFiles := 5000
	if raw := strings.TrimSpace(os.Getenv("DIFFMIND_JDTLS_MAX_FILES")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxFiles = v
		}
	}
	includeTests := strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_JDTLS_INCLUDE_TESTS")), "true")
	includeImportDeps := strings.EqualFold(strings.TrimSpace(os.Getenv("DIFFMIND_JDTLS_INCLUDE_IMPORT_DEPENDENCIES")), "true")

	files, err := collectJavaFiles(sourceRoot, maxFiles, includeTests)
	if err != nil {
		return "", adapterSemanticDocument{}, err
	}
	if len(files) == 0 {
		return "jdtls lsp: no java files detected", adapterSemanticDocument{}, nil
	}
	if strings.TrimSpace(toolPath) == "" {
		toolPath = "jdtls"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	workspaceDir := filepath.Join(os.TempDir(), "diffmind-jdtls-"+jdtlsStableID(sourceRoot))
	client, err := startJDTLSClient(ctx, toolPath, sourceRoot, workspaceDir)
	if err != nil {
		return "", adapterSemanticDocument{}, err
	}
	defer client.Close()

	if err := client.Initialize(ctx, sourceRoot); err != nil {
		return "", adapterSemanticDocument{}, err
	}

	pkg := adapterSemanticPackage{
		Name:  "java-lsp",
		Files: make([]adapterSemanticFile, 0, len(files)),
	}
	symbolCount := 0
	depCount := 0
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
		linesByPath[rel] = strings.Split(string(content), "\n")

		_ = client.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": "java",
				"version":    1,
				"text":       string(content),
			},
		})

		raw, err := client.Request(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}, 6*time.Second)
		if err != nil {
			requestErrors++
			continue
		}

		symbols := parseJDTLSDocumentSymbols(raw, rel, content)
		deps := []adapterSemanticImport(nil)
		if includeImportDeps {
			deps = parseJavaImportDependencies(rel, content)
		}
		if len(symbols) == 0 && len(deps) == 0 {
			continue
		}

		symbolCount += len(symbols)
		depCount += len(deps)
		pkg.Files = append(pkg.Files, adapterSemanticFile{
			Path:         rel,
			Symbols:      symbols,
			Dependencies: deps,
		})

		_ = client.Notify("textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
	}

	xrefCalls, xrefErrors := enrichPackageWithLSPReferences(ctx, client, sourceRoot, &pkg, linesByPath)

	_ = client.Shutdown(ctx)
	if len(pkg.Files) == 0 {
		return fmt.Sprintf("jdtls lsp: files=%d symbols=%d dependencies=%d xref_calls=%d request_errors=%d xref_errors=%d include_import_dependencies=%t", len(files), symbolCount, depCount, xrefCalls, requestErrors, xrefErrors, includeImportDeps), adapterSemanticDocument{}, nil
	}

	doc := adapterSemanticDocument{
		Packages: []adapterSemanticPackage{pkg},
	}
	logText := fmt.Sprintf("jdtls lsp: files=%d symbols=%d dependencies=%d xref_calls=%d request_errors=%d xref_errors=%d include_tests=%t include_import_dependencies=%t", len(pkg.Files), symbolCount, depCount, xrefCalls, requestErrors, xrefErrors, includeTests, includeImportDeps)
	return logText, doc, nil
}

func collectJavaFiles(root string, maxFiles int, includeTests bool) ([]string, error) {
	out := make([]string, 0, maxFiles)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d == nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".diffmind", ".idea", "node_modules", "target", "build", "out":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".java") {
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
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func parseJavaImportDependencies(relPath string, content []byte) []adapterSemanticImport {
	lines := strings.Split(string(content), "\n")
	out := make([]adapterSemanticImport, 0, 8)
	for i, line := range lines {
		m := javaImportLineRe.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		module := strings.TrimSpace(m[1])
		if module == "" {
			continue
		}
		out = append(out, adapterSemanticImport{
			Module:  module,
			Kind:    "java-import",
			File:    relPath,
			Line:    i + 1,
			Col:     1,
			Snippet: line,
		})
	}
	return out
}

func parseJDTLSDocumentSymbols(raw json.RawMessage, relPath string, content []byte) []adapterSemanticSymbol {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	lines := strings.Split(string(content), "\n")

	var docSymbols []lspDocumentSymbol
	if err := json.Unmarshal(raw, &docSymbols); err == nil && len(docSymbols) > 0 {
		out := make([]adapterSemanticSymbol, 0, len(docSymbols))
		var walk func(items []lspDocumentSymbol)
		walk = func(items []lspDocumentSymbol) {
			for _, s := range items {
				line := s.Range.Start.Line + 1
				col := s.Range.Start.Character + 1
				snippet := ""
				if line >= 1 && line <= len(lines) {
					snippet = lines[line-1]
				}
				out = append(out, adapterSemanticSymbol{
					Name:    strings.TrimSpace(s.Name),
					Kind:    lspSymbolKindToName(s.Kind),
					File:    relPath,
					Line:    line,
					Col:     col,
					Snippet: snippet,
				})
				if len(s.Children) > 0 {
					walk(s.Children)
				}
			}
		}
		walk(docSymbols)
		return out
	}

	var infos []lspSymbolInformation
	if err := json.Unmarshal(raw, &infos); err == nil && len(infos) > 0 {
		out := make([]adapterSemanticSymbol, 0, len(infos))
		for _, info := range infos {
			line := info.Location.Range.Start.Line + 1
			col := info.Location.Range.Start.Character + 1
			snippet := ""
			if line >= 1 && line <= len(lines) {
				snippet = lines[line-1]
			}
			out = append(out, adapterSemanticSymbol{
				Name:    strings.TrimSpace(info.Name),
				Kind:    lspSymbolKindToName(info.Kind),
				File:    relPath,
				Line:    line,
				Col:     col,
				Snippet: snippet,
			})
		}
		return out
	}
	return nil
}

func lspSymbolKindToName(kind int) string {
	switch kind {
	case 5:
		return "class"
	case 6:
		return "method"
	case 9:
		return "constructor"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 8:
		return "field"
	default:
		return "symbol"
	}
}

func startJDTLSClient(ctx context.Context, toolPath string, sourceRoot string, workspaceDir string) (*lspClient, error) {
	if strings.TrimSpace(toolPath) == "" {
		toolPath = "jdtls"
	}
	_ = os.MkdirAll(workspaceDir, 0o755)
	return startLSPClient(ctx, toolPath, []string{"-data", workspaceDir}, sourceRoot)
}

func (c *lspClient) Initialize(ctx context.Context, sourceRoot string) error {
	rootURI := pathToFileURI(sourceRoot)
	_, err := c.Request(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name":    "diffmind",
			"version": analyzerVersion,
		},
		"rootUri": rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
		},
		"workspaceFolders": []map[string]any{
			{"uri": rootURI, "name": filepath.Base(sourceRoot)},
		},
	}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("jdtls initialize: %w", err)
	}
	if err := c.Notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("jdtls initialized notification: %w", err)
	}
	return nil
}

func (c *lspClient) Shutdown(ctx context.Context) error {
	_, err := c.Request(ctx, "shutdown", map[string]any{}, 5*time.Second)
	if err != nil {
		return err
	}
	_ = c.Notify("exit", map[string]any{})
	return nil
}

func (c *lspClient) Close() {
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-done
	}
}

func (c *lspClient) stderrText() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return c.stderr.String()
}

func (c *lspClient) readLoop() {
	defer close(c.readDone)
	for {
		payload, err := readLSPPayload(c.stdout)
		if err != nil {
			c.failPending(err)
			c.readDone <- err
			return
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		idRaw, hasID := msg["id"]
		_, hasMethod := msg["method"]
		if !hasID || hasMethod {
			continue
		}
		key := lspIDKey(idRaw)
		if key == "" {
			continue
		}
		resp := lspResponse{Result: msg["result"]}
		if errRaw, ok := msg["error"]; ok && len(errRaw) > 0 && string(errRaw) != "null" {
			var rpcErr lspRPCError
			if json.Unmarshal(errRaw, &rpcErr) == nil {
				resp.Error = &rpcErr
			} else {
				resp.Error = &lspRPCError{Code: -32000, Message: string(errRaw)}
			}
		}

		c.pendingMu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- resp
			close(ch)
		}
	}
}

func (c *lspClient) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for key, ch := range c.pending {
		ch <- lspResponse{Error: &lspRPCError{Code: -32001, Message: err.Error()}}
		close(ch)
		delete(c.pending, key)
	}
}

func (c *lspClient) Request(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	key := strconv.FormatInt(id, 10)
	respCh := make(chan lspResponse, 1)

	c.pendingMu.Lock()
	c.pending[key] = respCh
	c.pendingMu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.sendMessage(msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, err
	}

	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("lsp request timeout: %s", method)
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("lsp request channel closed: %s", method)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp request failed %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *lspClient) Notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.sendMessage(msg)
}

func (c *lspClient) sendMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func readLSPPayload(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			raw := strings.TrimSpace(line[len("content-length:"):])
			if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
				contentLength = v
			}
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content-length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func lspIDKey(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	switch id := v.(type) {
	case string:
		return strings.TrimSpace(id)
	case float64:
		return strconv.FormatInt(int64(id), 10)
	default:
		return ""
	}
}

func pathToFileURI(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		if p, err := filepath.Abs(abs); err == nil {
			abs = p
		}
	}
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return "file://" + abs
}

func jdtlsStableID(v string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(v)))
	return hex.EncodeToString(sum[:8])
}
