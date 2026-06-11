package pipeline

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func TestForceObjectiveTypeCanonicalizesAlias(t *testing.T) {
	obj := objectives.Objective{Kind: model.KindDependency, Type: "outbound_http"}
	e := &llmEntity{Type: "outbound_http_service", Name: "Boost API"}
	if !ForceObjectiveType(obj, e) {
		t.Fatal("expected alias to be accepted")
	}
	if e.Type != "outbound_http" {
		t.Fatalf("type = %q, want outbound_http", e.Type)
	}
}

func TestForceObjectiveTypeRejectsWrongCategory(t *testing.T) {
	obj := objectives.Objective{Kind: model.KindDependency, Type: "queue_publish"}
	e := &llmEntity{Type: "command_exec", Name: "SQS send"}
	if ForceObjectiveType(obj, e) {
		t.Fatal("expected wrong category to be rejected")
	}
	if e.Type != "queue_publish" {
		t.Fatalf("type should still be canonicalized to objective, got %q", e.Type)
	}
}

func TestEnrichEntityGroupingDerivesOutboundHTTPInstance(t *testing.T) {
	b := model.BaseEntity{
		Type: "outbound_http",
		Name: "Pricing Calculator API",
		Details: map[string]any{
			"method":       "PUT",
			"path":         "/traffic-info/{id}/placements",
			"target_url":   "https://pricing-service.example.global",
			"client_class": "BoostFactorCalculatorClient",
		},
	}
	EnrichEntityGrouping(&b)
	if b.Platform != "http" {
		t.Fatalf("platform = %q, want http", b.Platform)
	}
	if b.Instance != "pricing-service.example.global" {
		t.Fatalf("instance = %q", b.Instance)
	}
	if b.Operation != "PUT /traffic-info/{id}/placements" {
		t.Fatalf("operation = %q", b.Operation)
	}
}
