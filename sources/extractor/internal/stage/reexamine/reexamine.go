// Package reexamine owns the policy that decides whether a discovered
// candidate requires another LLM verification pass.
package reexamine

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

type Runner struct{}

type Input struct {
	Objective     objectives.Objective
	Candidate     extraction.Candidate
	MinConfidence float64
}

type Output struct {
	Candidate  extraction.Candidate
	ReasonCode string
	Reason     string
	Needed     bool
}

func (Runner) Run(input Input) Output {
	candidate := input.Candidate
	code, reason, needed := extraction.ShouldReexamine(input.Objective, &candidate, input.MinConfidence)
	return Output{Candidate: candidate, ReasonCode: code, Reason: reason, Needed: needed}
}
