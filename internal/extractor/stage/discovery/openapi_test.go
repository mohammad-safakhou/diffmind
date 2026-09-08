package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestOpenAPIContractsLocalRefsAndLocations(t *testing.T) {
	repo := t.TempDir()
	body := `openapi: 3.0.3
paths:
  /v1/items/{id}:
    patch:
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string}
        - name: dryRun
          in: query
          schema: {type: boolean}
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/UpdateItem'
components:
  schemas:
    UpdateItem:
      type: object
      required: [name]
      properties:
        name: {type: string}
        experimentGroup: {type: string, nullable: true}
`
	if err := os.WriteFile(filepath.Join(repo, "openapi.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exposures := []model.Exposure{{BaseEntity: model.BaseEntity{ID: "route", Name: "PATCH /v1/items/{id}", Details: map[string]any{"method": "PATCH", "path": "/v1/items/{id}"}}}}
	if warnings := EnrichHTTPContractsFromOpenAPI(repo, exposures); len(warnings) != 0 {
		t.Fatalf("warnings=%v", warnings)
	}
	fields, ok := exposures[0].Details["request_fields"].([]any)
	if !ok || len(fields) != 4 {
		t.Fatalf("fields=%#v", exposures[0].Details["request_fields"])
	}
	found := map[string]map[string]any{}
	for _, raw := range fields {
		field := raw.(map[string]any)
		found[field["name"].(string)] = field
	}
	if found["id"]["location"] != "path" || found["id"]["required"] != true || found["dryRun"]["type"] != "boolean" || found["experimentGroup"]["nullable"] != true || found["name"]["required"] != true || found["name"]["source_file"] != "openapi.yaml" || found["name"]["source_line"].(int) <= 0 {
		t.Fatalf("fields=%+v", found)
	}
}

func TestOpenAPIContractsFailClosed(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "openapi.yaml"), []byte("openapi: 3.0.3\npaths: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	exposures := []model.Exposure{{BaseEntity: model.BaseEntity{Name: "GET /items"}}}
	if warnings := EnrichHTTPContractsFromOpenAPI(repo, exposures); len(warnings) != 1 {
		t.Fatalf("warnings=%v", warnings)
	}
	if exposures[0].Details != nil {
		t.Fatalf("malformed contract mutated exposure: %+v", exposures[0])
	}
}
