package infrastructure

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestRunnerParsesInventory(t *testing.T) {
	index := &ast.ProjectIndex{Configs: map[string]*ast.ConfigFile{
		"application.yml": {Entries: []ast.ConfigEntry{{Key: "db.url", Value: "postgres://db/orders"}}},
	}}
	runner := Runner{Prompt: func(_ context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
		if role != "infrastructure" || prompt == "" || schema["type"] != "object" {
			t.Fatalf("invalid prompt call")
		}
		return map[string]any{"databases": []any{map[string]any{
			"name": "orders", "kind": "database", "system": "postgres",
		}}}, nil
	}}
	inventory, err := runner.Run(context.Background(), Input{Index: index})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Databases) != 1 || inventory.Databases[0].System != "postgres" {
		t.Fatalf("inventory = %+v", inventory)
	}
}
