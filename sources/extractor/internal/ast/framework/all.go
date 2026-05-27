// Package framework exports all framework detectors. Import this package
// with a blank identifier to register all detectors with ast.RegisterFrameworkDetector:
//
//	import _ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
//
// This is intentionally empty of runtime code — all registration happens in
// individual detector files' init() functions.
package framework
