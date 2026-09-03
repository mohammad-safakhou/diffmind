package knowledge

import (
	"fmt"
	"regexp"
	"strings"
)

var detectorTypes = map[string]bool{
	"outbound_http": true, "outbound_rpc": true,
	"queue_publish": true, "queue_consumer": true,
}

func validateDetectors(pack *Pack) []ValidationError {
	var errs []ValidationError
	names := map[string]bool{}
	for i, rule := range pack.Detectors {
		base := fmt.Sprintf("detectors[%d]", i)
		add := func(field, message string) {
			errs = append(errs, ValidationError{Field: base + "." + field, Message: message})
		}
		if strings.TrimSpace(rule.Name) == "" || names[rule.Name] {
			add("name", "must be nonempty and unique")
		}
		names[rule.Name] = true
		if !detectorTypes[rule.Type] {
			add("type", "want outbound_http, outbound_rpc, queue_publish, or queue_consumer")
		}
		if !safeRelativePath(rule.Source.Glob) {
			add("source.glob", "must be a safe relative glob")
		}
		if strings.ContainsAny(rule.Source.Glob, "[]{}") {
			add("source.glob", "only *, ** and ? wildcards are supported")
		}
		switch rule.Strategy {
		case "", "field_path":
			if rule.Field == "" || rule.Pattern != "" {
				add("field", "field_path requires field and forbids pattern")
			}
			for _, part := range strings.Split(rule.Field, ".") {
				if part == "" {
					add("field", "field path components must not be empty")
					break
				}
			}
		case "regex":
			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				add("pattern", "invalid regular expression: "+err.Error())
			} else if re.SubexpIndex("target") < 1 {
				add("pattern", "requires a named (?P<target>...) capture")
			}
			if rule.Field != "" {
				add("field", "regex forbids field")
			}
		default:
			add("strategy", "want field_path or regex")
		}
	}
	if len(pack.Detectors) > 0 && pack.AppliesTo.Kind != "service_repo" {
		errs = append(errs, ValidationError{Field: "applies_to.kind", Message: "relationship detectors currently require service_repo"})
	}
	names = map[string]bool{}
	for _, test := range pack.Tests {
		if names[test.Name] {
			errs = append(errs, ValidationError{Field: "tests", Message: "duplicate test name: " + test.Name})
		}
		names[test.Name] = true
	}
	for i, test := range pack.GraphTests {
		base := fmt.Sprintf("graph_tests[%d]", i)
		if test.Name == "" || names[test.Name] {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "must be nonempty and unique across tests"})
		}
		names[test.Name] = true
		if len(test.Repositories) == 0 {
			errs = append(errs, ValidationError{Field: base + ".repositories", Message: "at least one fixture repository is required"})
		}
		services := map[string]bool{}
		for _, repo := range test.Repositories {
			if !packIDPattern.MatchString(repo.Name) || services[repo.Name] || !safeRelativePath(repo.Fixture) {
				errs = append(errs, ValidationError{Field: base + ".repositories", Message: "require unique lowercase service names and safe fixture paths"})
			}
			services[repo.Name] = true
		}
		seenEdges := map[ExpectedEdge]bool{}
		for _, edge := range test.Edges {
			if edge.From == "" || edge.To == "" || edge.Type == "" || seenEdges[edge] {
				errs = append(errs, ValidationError{Field: base + ".edges", Message: "edges require from, to, type and must be unique"})
			}
			seenEdges[edge] = true
		}
	}
	return errs
}
