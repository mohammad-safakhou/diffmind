package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to <snap>/<rel> and returns the snap dir.
func writeFile(t *testing.T, snap, rel, content string) {
	t.Helper()
	full := filepath.Join(snap, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// helper for one-shot extraction.
func extractAt(t *testing.T, snap, file string, line int32, language string) []Condition {
	t.Helper()
	e := NewConditionExtractor(snap)
	return e.Extract(CallSite{
		At: Location{File: file, StartLine: line},
	}, language)
}

func hasKind(cs []Condition, k ConditionKind) bool {
	for _, c := range cs {
		if c.Kind == k {
			return true
		}
	}
	return false
}

// ---- Java ----

func TestConditionsJavaIfGuard(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "Foo.java", `package x;
public class Foo {
    public void m(String s) {
        if (s != null && !s.isEmpty()) {
            repo.save(s);
        }
    }
}`)
	cs := extractAt(t, snap, "Foo.java", 4, "java") // line 4 (zero-based) is `repo.save(s)`
	if !hasKind(cs, ConditionIfGuard) {
		t.Errorf("expected if_guard, got %+v", cs)
	}
}

func TestConditionsJavaPreAuthorize(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "Ctrl.java", `package x;
@RestController
public class Ctrl {

    @PreAuthorize("hasRole('ADMIN')")
    @DeleteMapping("/x/{id}")
    public void delete(@PathVariable String id) {
        service.softDelete(id);
    }
}`)
	cs := extractAt(t, snap, "Ctrl.java", 7, "java") // line 7 = `service.softDelete(id);`
	if !hasKind(cs, ConditionAuth) {
		t.Errorf("expected auth condition for @PreAuthorize, got %+v", cs)
	}
}

func TestConditionsJavaOptionalFilter(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "Foo.java", `package x;
public class Foo {
    public void m(Optional<User> u) {
        u.filter(x -> x.isActive()).ifPresent(repo::touch);
    }
}`)
	cs := extractAt(t, snap, "Foo.java", 3, "java")
	if !hasKind(cs, ConditionOptional) {
		t.Errorf("expected optional condition, got %+v", cs)
	}
}

// ---- TypeScript ----

func TestConditionsTSOptionalChaining(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "svc.ts", `export function run(user?: User) {
  user?.profile.update();
}`)
	cs := extractAt(t, snap, "svc.ts", 1, "typescript")
	if !hasKind(cs, ConditionOptional) {
		t.Errorf("expected optional, got %+v", cs)
	}
}

func TestConditionsTSIfGuard(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "svc.ts", `export function run(x: string) {
  if (x && x.length > 0) {
    repo.save(x);
  }
}`)
	cs := extractAt(t, snap, "svc.ts", 2, "typescript")
	if !hasKind(cs, ConditionIfGuard) {
		t.Errorf("expected if_guard, got %+v", cs)
	}
}

// ---- Python ----

func TestConditionsPythonIfGuard(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "svc.py", `def run(x):
    if x is not None and len(x) > 0:
        repo.save(x)
`)
	cs := extractAt(t, snap, "svc.py", 2, "python")
	if !hasKind(cs, ConditionIfGuard) {
		t.Errorf("expected if_guard, got %+v", cs)
	}
}

func TestConditionsPythonFastAPIDepends(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "main.py", `@app.delete("/x/{id}")
def delete(
    id: str,
    current_user: User = Depends(get_current_user),
):
    service.softDelete(id)
`)
	cs := extractAt(t, snap, "main.py", 5, "python")
	if !hasKind(cs, ConditionAuth) {
		t.Errorf("expected auth condition for Depends(get_current_user), got %+v", cs)
	}
}

// ---- Go ----

func TestConditionsGoIfGuard(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "x.go", `package x
func Run(s string) {
    if s != "" {
        repo.Save(s)
    }
}`)
	cs := extractAt(t, snap, "x.go", 3, "go")
	if !hasKind(cs, ConditionIfGuard) {
		t.Errorf("expected if_guard, got %+v", cs)
	}
}

// ---- Edge cases ----

func TestConditionsMissingFileReturnsNil(t *testing.T) {
	snap := t.TempDir()
	cs := extractAt(t, snap, "does-not-exist.java", 5, "java")
	if cs != nil {
		t.Errorf("expected nil for missing file, got %+v", cs)
	}
}

func TestConditionsDedupesIdentical(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "x.go", `package x
func Run() {
    if a == nil {
        if a == nil { // duplicate textual guard
            repo.Save()
        }
    }
}`)
	cs := extractAt(t, snap, "x.go", 4, "go")
	// We expect at most one if_guard with expression "a == nil",
	// even though two textual matches exist.
	count := 0
	for _, c := range cs {
		if c.Kind == ConditionIfGuard {
			count++
		}
	}
	if count > 1 {
		t.Errorf("expected dedup of duplicate if guards, got %d: %+v", count, cs)
	}
}

func TestConditionsLoopDetection(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "x.go", `package x
func Run(items []string) {
    for _, it := range items {
        repo.Save(it)
    }
}`)
	cs := extractAt(t, snap, "x.go", 3, "go")
	if !hasKind(cs, ConditionLoop) {
		t.Errorf("expected loop, got %+v", cs)
	}
}

func TestConditionsTryCatch(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, snap, "Foo.java", `package x;
public class Foo {
    public void m() {
        try {
            repo.save();
        } catch (Exception e) {}
    }
}`)
	cs := extractAt(t, snap, "Foo.java", 4, "java")
	if !hasKind(cs, ConditionExceptionPath) {
		t.Errorf("expected exception path, got %+v", cs)
	}
}
