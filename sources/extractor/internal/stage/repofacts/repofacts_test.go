package repofacts

import (
	"context"
	"testing"
)

func TestRunnerUsesStableRolePromptAndSchema(t *testing.T) {
	var role, prompt string
	runner := Runner{Prompt: func(_ context.Context, gotRole, gotPrompt string, schema map[string]any) (map[string]any, error) {
		role, prompt = gotRole, gotPrompt
		if schema["type"] != "object" {
			t.Fatalf("schema = %#v", schema)
		}
		return map[string]any{"service_name": "orders", "languages": []any{"Go"}}, nil
	}}
	out, err := runner.Run(context.Background(), Input{SubDir: "services/orders", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if role != "repo_facts" || prompt != BuildPrompt("services/orders") {
		t.Fatalf("role=%q prompt mismatch=%v", role, prompt != BuildPrompt("services/orders"))
	}
	if out.Facts == nil || out.Facts.ServiceName != "orders" {
		t.Fatalf("facts = %+v", out.Facts)
	}
}
