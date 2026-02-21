package graph

import (
	"testing"

	"diffmind/internal/bundleio"
)

func TestPreferredConfigResolvedValueSkipsNilSentinelValues(t *testing.T) {
	entities := []bundleio.Entity{
		{
			Type: "ConfigKey",
			Attributes: map[string]any{
				"key":            "services.aws.sqs.catalog-target-request-sqs",
				"environment":    "prod",
				"resolved_value": nil,
			},
		},
		{
			Type: "ConfigKey",
			Attributes: map[string]any{
				"key":            "services.aws.sqs.catalog-target-request-sqs",
				"environment":    "prod",
				"resolved_value": "https://sqs.eu-west-1.amazonaws.com/123456789012/queue",
			},
		},
	}

	got := preferredConfigResolvedValue(entities, "services.aws.sqs.catalog-target-request-sqs")
	if got != "https://sqs.eu-west-1.amazonaws.com/123456789012/queue" {
		t.Fatalf("expected non-nil resolved value, got %q", got)
	}
}

