// Package framework provides per-framework implicit-invocation detectors.
// Each detector recognises the framework-specific patterns that cause methods
// to be called without a direct syntactic call in user code — HTTP route
// handlers, scheduled jobs, event listeners, queue consumers, middleware, etc.
//
// All detectors are registered via their init() functions, which call
// ast.RegisterFrameworkDetector. The ast.Build() function collects bindings
// from all registered detectors after the project index is fully built.
//
// Adding a new framework:
//  1. Create a new file in this package.
//  2. Define a struct implementing ast.FrameworkDetector.
//  3. Call ast.RegisterFrameworkDetector(&myDetector{}) from an init() function.
//  4. Import this package (or the specific file) from wherever ast.Build is called.
package framework

import "github.com/mohammad-safakhou/diffmind/internal/ast"

// register is a package-local alias for ast.RegisterFrameworkDetector, used
// by all detector init() functions in this package.
func register(d ast.FrameworkDetector) {
	ast.RegisterFrameworkDetector(d)
}
