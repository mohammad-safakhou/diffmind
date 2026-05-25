package scip

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConditionKind classifies a guard or branch found preceding a call
// site. The string values match what we serialise into
// model.Condition.Kind so downstream consumers (artifacts, UI) don't
// need a translation table.
type ConditionKind string

const (
	ConditionIfGuard       ConditionKind = "if_guard"
	ConditionTernary       ConditionKind = "ternary"
	ConditionOptional      ConditionKind = "optional"      // Java Optional.filter / TS optional chaining
	ConditionNullCheck     ConditionKind = "null_check"
	ConditionExceptionPath ConditionKind = "exception"     // wrapped in try/catch
	ConditionLoop          ConditionKind = "loop"          // iterated over a collection
	ConditionAuth          ConditionKind = "auth"          // @PreAuthorize / @Secured / FastAPI Depends(auth)
	ConditionFeatureFlag   ConditionKind = "feature_flag"  // toggle/flag-style call
)

// Condition is the deterministic equivalent of model.Condition. We
// expose it as a separate struct from the proto-derived types in this
// package so callers can convert at the diffmind boundary without
// re-importing the model package here (we keep internal/scip
// dependency-light for clarity).
type Condition struct {
	Kind        ConditionKind
	Expression  string
	Explanation string
	// Line is the source line (zero-based) where the guard expression
	// starts. Used by the UI to highlight the source.
	Line int32
}

// ConditionExtractor inspects source text around a call site and
// returns the syntactic guards that gate it. The extractor is
// LANGUAGE-AGNOSTIC by design: it works on textual patterns shared
// across most C-family / Python / Java / TS sources. Language-specific
// extensions (annotations, decorators) live in per-language tables
// below and are matched only when the document language matches.
//
// LIMITATIONS
//
// We are doing line-oriented pattern matching, not parsing. We catch
// the common cases (`if (...)`, `if cond:`, `Optional.filter`, etc.)
// and ignore complex constructs (multi-line conditional expressions
// split across many lines, lambda bodies, etc.). The LLM that
// previously did this job was better at edge cases, but it was also
// 8 minutes slower and unreliable.
type ConditionExtractor struct {
	// snapshotPath is the absolute path to the source tree root. We
	// use it to resolve a SCIP relative_path to a real file we can
	// read. Required.
	snapshotPath string

	// fileCache memoises file contents to avoid re-reading the same
	// file once per call site. Keyed by relative path.
	fileCache map[string][]string
}

// NewConditionExtractor binds an extractor to a snapshot directory.
func NewConditionExtractor(snapshotPath string) *ConditionExtractor {
	return &ConditionExtractor{
		snapshotPath: snapshotPath,
		fileCache:    map[string][]string{},
	}
}

// Extract returns the conditions that gate the given call site. The
// `language` hint refines which patterns we apply (Java annotations
// only fire on .java files, FastAPI's Depends() only on .py, etc.).
// Passing "" runs only the language-agnostic patterns.
//
// We look at:
//   - the line of the call site itself (inline ternary, optional chaining)
//   - up to 20 lines BEFORE the call (preceding if/try/for blocks)
//   - the annotation lines on the enclosing function definition (auth, etc.)
//
// The 20-line backwards window is intentional: it catches the
// "validate inputs, then call" pattern that dominates real codebases
// while avoiding pulling in unrelated logic from earlier in the method.
func (e *ConditionExtractor) Extract(site CallSite, language string) []Condition {
	if e == nil || site.At.File == "" {
		return nil
	}
	lines := e.fileLines(site.At.File)
	if len(lines) == 0 {
		return nil
	}
	out := []Condition{}

	siteLine := int(site.At.StartLine)
	if siteLine < 0 {
		siteLine = 0
	}
	if siteLine >= len(lines) {
		return nil
	}

	// 1. Inline patterns on the call line itself.
	cur := lines[siteLine]
	out = append(out, scanInlinePatterns(cur, siteLine, language)...)

	// 2. Preceding lines: 20-line backwards window.
	start := siteLine - 20
	if start < 0 {
		start = 0
	}
	out = append(out, scanPrecedingLines(lines[start:siteLine], start, language)...)

	// 3. Annotation lines on the enclosing definition (auth checks etc.).
	// We approximate "enclosing definition" by walking back to the
	// nearest line that ends in "{" or ":" at indentation 0–8 (function
	// signature). This is fast and gets us 95% of cases in practice.
	out = append(out, scanEnclosingAnnotations(lines, siteLine, language)...)

	return dedupeConditions(out)
}

// fileLines lazily reads a file and caches it. Returns nil on any
// I/O error (logged but non-fatal — a missing file means we lose
// some condition extraction for that path, not the entire walk).
func (e *ConditionExtractor) fileLines(relativePath string) []string {
	if lines, ok := e.fileCache[relativePath]; ok {
		return lines
	}
	full := filepath.Join(e.snapshotPath, filepath.FromSlash(relativePath))
	f, err := os.Open(full)
	if err != nil {
		e.fileCache[relativePath] = nil
		return nil
	}
	defer f.Close()
	lines := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024) // 8 MB max line — handles minified bundles
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	e.fileCache[relativePath] = lines
	return lines
}

// ---------------------------------------------------------------------
// Pattern tables.
//
// Each table groups a regex with a ConditionKind. Patterns are
// compiled once at package init for performance: the extractor is
// called in the hot path of the connections stage, hundreds to
// thousands of times per run.
// ---------------------------------------------------------------------

var (
	// reIfStmt matches `if (cond)` style guards used in Java, C/C++,
	// JS, TS, Kotlin.
	reIfStmt = regexp.MustCompile(`(?m)\bif\s*\(([^)]+)\)`)
	// reGoRustIf matches `if expr {` style used by Go, Rust, Kotlin.
	// We require a trailing brace to avoid false positives on the
	// keyword used inside string literals or comments.
	reGoRustIf = regexp.MustCompile(`(?m)^\s*if\s+([^{]+)\{`)
	// rePythonIf matches Python's `if x is None:` style.
	rePythonIf = regexp.MustCompile(`(?m)^\s*(?:el(?:se )?if|if)\s+([^:]+):`)
	// reTernary matches ?: ternaries inline.
	reTernary = regexp.MustCompile(`([^?]+)\?([^:]+):`)
	// reOptionalFilter is Java's Optional<...>.filter(...) / .map(...) pattern.
	reOptionalFilter = regexp.MustCompile(`Optional\.\w+|\.filter\s*\(([^)]+)\)`)
	// reOptionalChain is TS/JS optional chaining.
	reOptionalChain = regexp.MustCompile(`\?\.`)
	// reNullCheck is the "if foo == null" / "if !foo" idiom.
	reNullCheck = regexp.MustCompile(`\b(\w+)\s*(==|!=)\s*(null|None|nil|undefined)\b|\bif\s+!\s*(\w+)\b`)
	// reTryCatch matches the start of a try block, Java/JS/Python.
	reTryCatch = regexp.MustCompile(`(?m)^\s*try\s*[:{]`)
	// reForEach matches loop heads: for/while/forEach.
	reForEach = regexp.MustCompile(`(?m)^\s*(for\s|while\s|.*\.forEach\s*\()`)
	// reAuthAnnotation matches @PreAuthorize("...") / @Secured("...") /
	// @RolesAllowed(...) on the previous lines.
	reAuthAnnotation = regexp.MustCompile(`@(PreAuthorize|Secured|RolesAllowed|RequiresAuth|RequireAuth)\s*\(([^)]*)\)`)
	// reFastAPIDepends matches FastAPI's Depends(get_current_user)
	// in function signatures.
	reFastAPIDepends = regexp.MustCompile(`Depends\s*\(\s*(\w+)\s*\)`)
	// reFeatureFlag matches common feature-flag method invocations.
	reFeatureFlag = regexp.MustCompile(`(?:isEnabled|toggle|featureFlag|flag)\s*\(\s*["']([^"']+)["']\s*\)`)
)

func scanInlinePatterns(line string, lineNum int, language string) []Condition {
	out := []Condition{}
	if m := reTernary.FindStringSubmatch(line); m != nil {
		out = append(out, Condition{
			Kind:        ConditionTernary,
			Expression:  strings.TrimSpace(m[1]),
			Explanation: "Inline ternary; branch is taken conditionally",
			Line:        int32(lineNum),
		})
	}
	if reOptionalChain.MatchString(line) {
		out = append(out, Condition{
			Kind:        ConditionOptional,
			Expression:  trimOnce(line, 100),
			Explanation: "Optional chaining (?.); call skipped if receiver is null/undefined",
			Line:        int32(lineNum),
		})
	}
	if m := reOptionalFilter.FindStringSubmatch(line); m != nil && language == "java" {
		out = append(out, Condition{
			Kind:        ConditionOptional,
			Expression:  trimOnce(m[0], 100),
			Explanation: "Java Optional combinator; downstream call gated by predicate",
			Line:        int32(lineNum),
		})
	}
	return out
}

func scanPrecedingLines(lines []string, startLine int, language string) []Condition {
	out := []Condition{}
	for i, ln := range lines {
		absLine := startLine + i
		switch language {
		case "python", "ruby":
			if m := rePythonIf.FindStringSubmatch(ln); m != nil {
				out = append(out, Condition{
					Kind:        ConditionIfGuard,
					Expression:  strings.TrimSpace(m[1]),
					Explanation: "Statement gated by an enclosing `if` branch",
					Line:        int32(absLine),
				})
				continue
			}
		case "go", "rust", "kotlin":
			if m := reGoRustIf.FindStringSubmatch(ln); m != nil {
				out = append(out, Condition{
					Kind:        ConditionIfGuard,
					Expression:  strings.TrimSpace(m[1]),
					Explanation: "Statement gated by an enclosing `if expr {`",
					Line:        int32(absLine),
				})
				continue
			}
			// Fall through to paren style too in case Kotlin uses it.
			if m := reIfStmt.FindStringSubmatch(ln); m != nil {
				out = append(out, Condition{
					Kind:        ConditionIfGuard,
					Expression:  strings.TrimSpace(m[1]),
					Explanation: "Statement gated by an enclosing `if (...)`",
					Line:        int32(absLine),
				})
				continue
			}
		default:
			if m := reIfStmt.FindStringSubmatch(ln); m != nil {
				out = append(out, Condition{
					Kind:        ConditionIfGuard,
					Expression:  strings.TrimSpace(m[1]),
					Explanation: "Statement gated by an enclosing `if (...)`",
					Line:        int32(absLine),
				})
				continue
			}
		}
		if m := reNullCheck.FindStringSubmatch(ln); m != nil {
			out = append(out, Condition{
				Kind:        ConditionNullCheck,
				Expression:  strings.TrimSpace(m[0]),
				Explanation: "Statement gated by a null/None check",
				Line:        int32(absLine),
			})
		}
		if reTryCatch.MatchString(ln) {
			out = append(out, Condition{
				Kind:        ConditionExceptionPath,
				Expression:  strings.TrimSpace(ln),
				Explanation: "Statement runs inside a try block; exceptions short-circuit downstream calls",
				Line:        int32(absLine),
			})
		}
		if reForEach.MatchString(ln) {
			out = append(out, Condition{
				Kind:        ConditionLoop,
				Expression:  strings.TrimSpace(ln),
				Explanation: "Statement runs inside a loop; downstream call repeats per element",
				Line:        int32(absLine),
			})
		}
		if m := reFeatureFlag.FindStringSubmatch(ln); m != nil {
			out = append(out, Condition{
				Kind:        ConditionFeatureFlag,
				Expression:  m[1],
				Explanation: "Statement gated by a feature flag",
				Line:        int32(absLine),
			})
		}
	}
	return out
}

// scanEnclosingAnnotations walks backwards until we find the enclosing
// function definition, then inspects the annotations / decorators
// directly above it.
func scanEnclosingAnnotations(lines []string, callLine int, language string) []Condition {
	out := []Condition{}
	// 1. Find the enclosing method/function start. Heuristic: the
	// nearest preceding line whose trimmed prefix matches a language
	// signature pattern. We're forgiving here — false positives cost
	// us one or two spurious annotations, not correctness.
	defLine := -1
	for i := callLine - 1; i >= 0 && i >= callLine-80; i-- {
		s := strings.TrimSpace(lines[i])
		if isFunctionSignature(s, language) {
			defLine = i
			break
		}
	}
	if defLine < 0 {
		return out
	}
	// 2. Inspect up to 10 lines above the definition for annotations
	// / decorators.
	start := defLine - 10
	if start < 0 {
		start = 0
	}
	for i := start; i < defLine; i++ {
		ln := lines[i]
		if m := reAuthAnnotation.FindStringSubmatch(ln); m != nil {
			out = append(out, Condition{
				Kind:        ConditionAuth,
				Expression:  strings.TrimSpace(m[0]),
				Explanation: "Call gated by an authorisation annotation on the enclosing method",
				Line:        int32(i),
			})
		}
	}
	// 3. Look for FastAPI Depends() in the def line itself (and the
	// 5 lines after, for multi-line signatures).
	if language == "python" {
		end := defLine + 5
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for i := defLine; i <= end; i++ {
			if m := reFastAPIDepends.FindStringSubmatch(lines[i]); m != nil {
				out = append(out, Condition{
					Kind:        ConditionAuth,
					Expression:  m[0],
					Explanation: "Call gated by a FastAPI dependency / auth resolver",
					Line:        int32(i),
				})
			}
		}
	}
	return out
}

// isFunctionSignature is a coarse classifier used to find an enclosing
// definition. We accept multiple syntactic shapes per language.
func isFunctionSignature(line string, language string) bool {
	switch language {
	case "python":
		return strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "async def ")
	case "ruby":
		return strings.HasPrefix(line, "def ")
	case "go":
		return strings.HasPrefix(line, "func ")
	case "java", "kotlin", "scala", "javascript", "typescript", "csharp":
		// Methods in these languages often end in "{". This is a rough
		// match: it catches `public void foo() {`, `function bar() {`,
		// `fun baz(): Int = {`, etc.
		return strings.HasSuffix(line, "{") && (strings.Contains(line, "(") || strings.Contains(line, "fun "))
	default:
		return strings.HasSuffix(line, "{") && strings.Contains(line, "(")
	}
}

// trimOnce trims whitespace and clamps to `max` characters.
func trimOnce(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// dedupeConditions removes near-duplicate conditions (same kind +
// expression). Order-preserving so the first occurrence wins.
func dedupeConditions(in []Condition) []Condition {
	if len(in) == 0 {
		return in
	}
	out := make([]Condition, 0, len(in))
	seen := map[string]struct{}{}
	for _, c := range in {
		key := string(c.Kind) + "|" + c.Expression
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}
