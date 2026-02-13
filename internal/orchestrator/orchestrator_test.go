package orchestrator

import (
	"errors"
	"testing"
)

func TestParseRunOptionsDefaults(t *testing.T) {
	opts, err := parseRunOptions(nil)
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if opts.Source != "." || opts.Ref != "HEAD" || opts.OutDir != ".diffmind" {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if !opts.Resume {
		t.Fatalf("resume should default to true")
	}
}

func TestClassifyErrorTaxonomy(t *testing.T) {
	cases := map[string]string{
		"dial tcp timeout":                       "timeout",
		"dial tcp 127.0.0.1: connection refused": "transient_network",
		"permission denied":                      "filesystem",
		"decode bundle failed":                   "data_contract",
		"something else":                         "unknown",
	}
	for msg, expected := range cases {
		got := classifyError(errors.New(msg))
		if got != expected {
			t.Fatalf("message %q expected %q got %q", msg, expected, got)
		}
	}
}
