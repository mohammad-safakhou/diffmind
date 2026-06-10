package agents

import (
	"fmt"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// deterministic_calls.go derives high-precision dependencies from call sites the
// AST already recorded — process execution and message publishing — the same
// way deterministicDBOperations derives db ops. Matching is gated on a curated
// set of (receiver, callee) patterns tied to a known library/template, so a
// generic ".send()" or ".exec()" is never mistaken for one (invariant #6).

// deterministicCommandExec finds process-execution dependencies (Runtime.exec,
// ProcessBuilder, Go os/exec.Command, Python subprocess/os.system).
func deterministicCommandExec(idx *astpkg.ProjectIndex) []llmEntity {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []llmEntity
	forEachCall(idx, func(cs astpkg.CallSite) {
		if !matchCommandExec(cs) {
			return
		}
		cmd := firstLiteralArg(cs.Arguments)
		name := cmd
		if name == "" {
			name = "exec in " + lastIdentOf(cs.Caller)
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := strings.ToLower(name) + "|" + loc.File
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		details := map[string]any{"discovered_by": "ast_exec_call"}
		if cmd != "" {
			details["command"] = cmd
		} else {
			details["command"] = name
		}
		out = append(out, llmEntity{
			Type:       "command_exec",
			Name:       name,
			Summary:    "AST-derived process execution",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "exec"},
			Details:    details,
			Locations:  []llmLocation{loc},
			Evidence:   []llmEvidence{callEvidence(cs)},
		})
	})
	return out
}

// deterministicQueuePublish finds message-publish dependencies through known
// publisher templates/clients (Spring Sqs/Sns/Kafka/Rabbit templates,
// StreamBridge, AWS SDK SQS/SNS clients). Only emitted when the destination is
// resolvable from a literal argument (or a ${...} config placeholder), so the
// emitted name is a real queue/topic, never a guess.
func deterministicQueuePublish(idx *astpkg.ProjectIndex) []llmEntity {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []llmEntity
	forEachCall(idx, func(cs astpkg.CallSite) {
		platform, ok := matchQueuePublish(cs)
		if !ok {
			return
		}
		dest := resolveResourceName(idx, firstLiteralArg(cs.Arguments))
		if dest == "" {
			return // destination not statically resolvable; leave to the LLM
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := platform + "|" + strings.ToLower(dest)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, llmEntity{
			Type:       "queue_publish",
			Name:       dest,
			Summary:    fmt.Sprintf("AST-derived %s publish to %s", platform, dest),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "messaging"},
			Details: map[string]any{
				"platform":      platform,
				"destination":   dest,
				"discovered_by": "ast_publish_call",
			},
			Locations: []llmLocation{loc},
			Evidence:  []llmEvidence{callEvidence(cs)},
		})
	})
	return out
}

// matchCommandExec reports whether a call site is a process execution, by a
// curated (receiver, callee) set. High precision: the receiver must tie to a
// known exec API, never a bare ".exec"/".run".
func matchCommandExec(cs astpkg.CallSite) bool {
	rr, cc := splitCall(cs)
	r := strings.ToLower(rr)
	c := strings.ToLower(cc)
	switch {
	case c == "exec" && strings.Contains(r, "runtime"): // Java Runtime.getRuntime().exec
		return true
	case c == "start" && strings.Contains(r, "processbuilder"): // Java new ProcessBuilder(...).start()
		return true
	case c == "command" && r == "exec": // Go os/exec.Command
		return true
	case r == "subprocess" && (c == "run" || c == "call" || c == "popen" || c == "check_output" || c == "check_call"): // Python
		return true
	case r == "os" && (c == "system" || c == "popen"): // Python os.system / os.popen
		return true
	}
	return false
}

// matchQueuePublish reports the messaging platform when a call site is a publish
// through a known template/client, else ("", false).
func matchQueuePublish(cs astpkg.CallSite) (string, bool) {
	rr, cc := splitCall(cs)
	r := strings.ToLower(rr)
	c := strings.ToLower(cc)
	type pat struct {
		token    string
		platform string
		callees  []string
	}
	pats := []pat{
		{"sqstemplate", "sqs", []string{"send", "sendmany"}},
		{"snstemplate", "sns", []string{"send", "sendnotification"}},
		{"kafkatemplate", "kafka", []string{"send"}},
		{"streambridge", "kafka", []string{"send"}},
		{"rabbittemplate", "rabbitmq", []string{"convertandsend", "send"}},
		{"sqsclient", "sqs", []string{"sendmessage", "sendmessagebatch"}},
		{"amazonsqs", "sqs", []string{"sendmessage", "sendmessagebatch"}},
		{"snsclient", "sns", []string{"publish"}},
		{"amazonsns", "sns", []string{"publish"}},
	}
	for _, p := range pats {
		if strings.Contains(r, p.token) && containsStr(p.callees, c) {
			return p.platform, true
		}
	}
	return "", false
}

// splitCall normalizes a call site into (receiver, method). Some language
// parsers fold the receiver into CalleeRaw (e.g. Go records "exec.Command"), so
// the method is the last dotted segment and the receiver is recovered from the
// prefix when ReceiverRaw is empty.
func splitCall(cs astpkg.CallSite) (receiver, method string) {
	method = strings.TrimSpace(cs.CalleeRaw)
	receiver = strings.TrimSpace(cs.ReceiverRaw)
	if i := strings.LastIndex(method, "."); i >= 0 {
		if receiver == "" {
			receiver = method[:i]
		}
		method = method[i+1:]
	}
	return receiver, method
}

// forEachCall visits every call site in the project's call graph.
func forEachCall(idx *astpkg.ProjectIndex, fn func(astpkg.CallSite)) {
	for _, sites := range idx.CallGraph {
		for _, cs := range sites {
			fn(cs)
		}
	}
}

func firstLiteralArg(args []astpkg.ArgumentExpr) string {
	for _, a := range args {
		if a.Kind == "literal" {
			return strings.Trim(strings.TrimSpace(a.Source), "\"'`")
		}
	}
	return ""
}

func callLoc(cs astpkg.CallSite) llmLocation {
	return llmLocation{File: cs.File, StartLine: int(cs.Range.StartLine) + 1, EndLine: int(cs.Range.EndLine) + 1}
}

func callEvidence(cs astpkg.CallSite) llmEvidence {
	snippet := strings.TrimSpace(strings.TrimSpace(cs.ReceiverRaw) + "." + strings.TrimSpace(cs.CalleeRaw))
	return llmEvidence{
		File:      cs.File,
		StartLine: int(cs.Range.StartLine) + 1,
		EndLine:   int(cs.Range.EndLine) + 1,
		Snippet:   strings.TrimPrefix(snippet, "."),
		Source:    "deterministic_ast",
	}
}

func lastIdentOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, "./#"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	if s == "" {
		return "handler"
	}
	return s
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
