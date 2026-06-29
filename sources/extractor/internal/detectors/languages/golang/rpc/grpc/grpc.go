package grpc

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Go gRPC server registration
//
// Generated protobuf packages expose Register<Service>Server functions. A call
// such as collector.RegisterMetadataServiceServer(server, impl) is the concrete
// server-side activation point for that RPC service.
type detector struct{}

func (d *detector) Name() string { return "go-grpc" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		for _, sym := range fa.Symbols {
			if b := goGRPCServerMethodToBinding(fa, sym); b != nil {
				out = append(out, *b)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, fa := range idx.Files {
		if fa.Language != "go" || !fileImportsGoGRPC(fa) {
			continue
		}
		for _, call := range fa.Calls {
			if b := goGRPCRegisterCallToBinding(call); b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func fileImportsGoGRPC(fa *ast.FileAST) bool {
	if fa == nil {
		return false
	}
	for _, imp := range fa.Imports {
		if strings.Trim(imp.Path, `"`) == "google.golang.org/grpc" {
			return true
		}
	}
	return false
}

func goGRPCServerMethodToBinding(fa *ast.FileAST, sym ast.SymbolDef) *ast.FrameworkBinding {
	if fa == nil || sym.Receiver == "" || sym.Name == "" {
		return nil
	}
	if strings.TrimSpace(sym.Receiver) != "Server" || sym.Name == "Init" || sym.Name == "New" {
		return nil
	}
	service := goGRPCServiceNameFromPath(fa.Path)
	if service == "" {
		return nil
	}
	p := strings.ToLower(strings.ReplaceAll(fa.Path, "\\", "/"))
	if !strings.Contains(p, "/adapter/inbound/") || !strings.Contains(p, "/grpc/") {
		return nil
	}
	return &ast.FrameworkBinding{
		Framework:        "go-grpc",
		Kind:             "rpc_endpoint",
		Direction:        "inbound",
		Symbol:           sym.Qualified,
		Trigger:          "grpc " + service + " " + sym.Name,
		TriggerSource:    sym.Qualified,
		File:             sym.File,
		Range:            sym.Range,
		ConfidenceReason: "go_grpc_inbound_server_method",
	}
}

func goGRPCRegisterCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	raw := strings.TrimSpace(call.CalleeRaw)
	verb := raw
	if dot := strings.LastIndex(verb, "."); dot >= 0 {
		verb = verb[dot+1:]
	}
	if !strings.HasPrefix(verb, "Register") || !strings.HasSuffix(verb, "Server") || len(call.Arguments) < 2 {
		return nil
	}
	service := strings.TrimSuffix(strings.TrimPrefix(verb, "Register"), "Server")
	if service == "" {
		return nil
	}
	symbol := call.Caller
	if h := frameworkutil.HandlerIdentifierArg(call.Arguments, 1); h != "" {
		symbol = h
	}
	return &ast.FrameworkBinding{
		Framework:        "go-grpc",
		Kind:             "rpc_endpoint",
		Direction:        "inbound",
		Symbol:           symbol,
		Trigger:          "grpc " + service + " *",
		TriggerSource:    raw + "(..., " + strings.TrimSpace(call.Arguments[1].Source) + ")",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_grpc_register_service_server",
	}
}

func goGRPCServiceNameFromPath(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	for _, part := range strings.Split(path, "/") {
		if strings.HasSuffix(part, "grpc") && len(part) > len("grpc") {
			return exportedCamel(strings.TrimSuffix(part, "grpc")) + "Service"
		}
	}
	return ""
}

func exportedCamel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}
