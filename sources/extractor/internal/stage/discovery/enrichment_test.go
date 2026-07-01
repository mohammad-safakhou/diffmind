package discovery

import (
	"os"
	"path/filepath"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func enrichmentFixtureIndex() *astpkg.ProjectIndex {
	return &astpkg.ProjectIndex{
		Files: map[string]*astpkg.FileAST{
			"src/OrderController.java": {
				Path:     "src/OrderController.java",
				Language: "java",
				Symbols: []astpkg.SymbolDef{
					{
						Name: "create", Qualified: "OrderController.create",
						Kind: astpkg.SymbolKindMethod, File: "src/OrderController.java",
						Range: astpkg.Range{StartLine: 33, EndLine: 38}, // 0-based → 1-based 34..39
						Annotations: []astpkg.Annotation{
							{Name: "PostMapping", Arguments: `"/orders"`, Range: astpkg.Range{StartLine: 33, EndLine: 33}},
							{Name: "PreAuthorize", Arguments: "hasRole('ADMIN')", Range: astpkg.Range{StartLine: 32, EndLine: 32}},
						},
					},
					{
						Name: "list", Qualified: "OrderController.list",
						Kind: astpkg.SymbolKindMethod, File: "src/OrderController.java",
						Range: astpkg.Range{StartLine: 45, EndLine: 50}, // 1-based 46..51
						Annotations: []astpkg.Annotation{
							{Name: "GetMapping", Arguments: `"/orders"`, Range: astpkg.Range{StartLine: 45, EndLine: 45}},
						},
					},
				},
			},
		},
	}
}

func TestEnrichExposuresFromAnnotations(t *testing.T) {
	idx := enrichmentFixtureIndex()
	exposures := []model.Exposure{
		{BaseEntity: model.BaseEntity{
			Type: "http_route", Name: "POST /orders",
			Locations: []model.Location{{File: "src/OrderController.java", StartLine: 34, EndLine: 39}},
		}},
		{BaseEntity: model.BaseEntity{
			Type: "http_route", Name: "GET /orders",
			Locations: []model.Location{{File: "src/OrderController.java", StartLine: 46, EndLine: 51}},
		}},
		{BaseEntity: model.BaseEntity{
			Type: "http_route", Name: "GET /preset",
			Details:   map[string]any{"auth": "already-set"},
			Locations: []model.Location{{File: "src/OrderController.java", StartLine: 34}},
		}},
	}

	EnrichExposuresFromAnnotations(idx, exposures)

	// Secured handler → auth rendered with args, authenticated=true.
	if got, _ := exposures[0].Details["auth"].(string); got != "PreAuthorize(hasRole('ADMIN'))" {
		t.Fatalf("auth = %q, want PreAuthorize(hasRole('ADMIN'))", got)
	}
	if a, _ := exposures[0].Details["authenticated"].(bool); !a {
		t.Fatal("authenticated = false, want true")
	}
	// Handler with only a routing annotation → no security fact stamped.
	if _, ok := exposures[1].Details["auth"]; ok {
		t.Fatalf("GET /orders should carry no auth detail, got %v", exposures[1].Details["auth"])
	}
	// Additive: a pre-existing auth value must never be overwritten.
	if got, _ := exposures[2].Details["auth"].(string); got != "already-set" {
		t.Fatalf("pre-set auth was overwritten: %q", got)
	}
}

func TestEnrichExposuresFromParams(t *testing.T) {
	idx := &astpkg.ProjectIndex{
		Files: map[string]*astpkg.FileAST{
			"src/OrderController.java": {
				Path: "src/OrderController.java", Language: "java",
				Symbols: []astpkg.SymbolDef{
					{
						Name: "get", Qualified: "OrderController.get", Kind: astpkg.SymbolKindMethod,
						File:  "src/OrderController.java",
						Range: astpkg.Range{StartLine: 9, EndLine: 12}, // 1-based 10..13
						Parameters: []astpkg.Param{
							{Name: "id", Type: "Long", Annotations: []astpkg.Annotation{{Name: "PathVariable", Arguments: `"id"`}}},
							{Name: "filter", Type: "String", Annotations: []astpkg.Annotation{{Name: "RequestParam", Arguments: "required = false"}}},
							{Name: "req", Type: "HttpServletRequest"}, // no binding → skipped
						},
					},
				},
			},
		},
	}
	exposures := []model.Exposure{
		{BaseEntity: model.BaseEntity{
			Type: "http_route", Name: "GET /orders/{id}",
			Locations: []model.Location{{File: "src/OrderController.java", StartLine: 10, EndLine: 13}},
		}},
		{BaseEntity: model.BaseEntity{
			Type:      "http_route",
			Name:      "GET /preset",
			Inputs:    []model.InputSpec{{Name: "preset"}},
			Locations: []model.Location{{File: "src/OrderController.java", StartLine: 10}},
		}},
	}

	EnrichExposuresFromParams(idx, exposures)

	if len(exposures[0].Inputs) != 2 {
		t.Fatalf("inputs = %d, want 2 (infra param skipped): %+v", len(exposures[0].Inputs), exposures[0].Inputs)
	}
	if in := exposures[0].Inputs[0]; in.Name != "id" || in.Type != "Long" || !in.Required || in.Description != "path" {
		t.Errorf("input0 = %+v, want id/Long/required/path", in)
	}
	if in := exposures[0].Inputs[1]; in.Name != "filter" || in.Required || in.Description != "query" {
		t.Errorf("input1 = %+v, want filter/optional/query", in)
	}
	// Additive: a route that already carries Inputs is left untouched.
	if len(exposures[1].Inputs) != 1 {
		t.Errorf("pre-set inputs were modified: %+v", exposures[1].Inputs)
	}
}

func TestEnrichExposuresFromAnnotationsNilIndexNoPanic(t *testing.T) {
	exposures := []model.Exposure{{BaseEntity: model.BaseEntity{Type: "http_route", Name: "GET /x"}}}
	EnrichExposuresFromAnnotations(nil, exposures) // must not panic
	if len(exposures[0].Details) != 0 {
		t.Fatalf("nil index should stamp nothing, got %v", exposures[0].Details)
	}
}

func TestEnrichHTTPContractsFromGoSwaggerHandler(t *testing.T) {
	root := t.TempDir()
	handlerFile := "internal/admin/payout_http/admin_list.go"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(handlerFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package payout_http

// AdminList godoc
//
//	@Param			opts	query	string	false	"opts"
//	@Success		200	{object}	http.ResponseWithMeta{data=[]PayoutListItemResponse,meta=http.ListMeta}
//	@Failure		401	{object}	http.ErrorResponse
//	@Failure		500	{object}	http.ErrorResponse
//	@Router			/admin/payouts [get]
func (c *Controller) AdminList(ctx echo.Context) error {
	return http.SuccessWithListMeta(ctx, nil, 0)
}
`
	if err := os.WriteFile(filepath.Join(root, handlerFile), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{
		RepoRoot: root,
		Files: map[string]*astpkg.FileAST{
			handlerFile: {
				Path:     handlerFile,
				Language: "go",
				Symbols: []astpkg.SymbolDef{{
					Name:      "AdminList",
					Qualified: "payout_http.Controller.AdminList",
					Kind:      astpkg.SymbolKindMethod,
					File:      handlerFile,
					Range:     astpkg.Range{StartLine: 9, EndLine: 11},
				}},
			},
		},
	}
	exposures := []model.Exposure{{BaseEntity: model.BaseEntity{
		Type: "http_route",
		Name: "GET /admin/payouts",
		Details: map[string]any{
			"handler": "c.AdminList",
			"method":  "GET",
			"path":    "/admin/payouts",
		},
		Locations: []model.Location{{File: "internal/admin/payout_http/controller.go", StartLine: 12, EndLine: 12}},
	}}}

	EnrichHTTPContractsFromHandlers(idx, exposures)

	if len(exposures[0].Inputs) != 1 {
		t.Fatalf("inputs = %+v, want opts query input", exposures[0].Inputs)
	}
	if in := exposures[0].Inputs[0]; in.Name != "opts" || in.Type != "string" || in.Required || in.Description != "query" {
		t.Fatalf("input = %+v, want optional query opts string", in)
	}
	responses, ok := exposures[0].Details["responses"].([]map[string]any)
	if !ok || len(responses) != 3 {
		t.Fatalf("responses = %#v, want 3 swagger responses", exposures[0].Details["responses"])
	}
	if responses[0]["status"] != 200 {
		t.Fatalf("first response = %#v, want status 200", responses[0])
	}
	if schema, _ := responses[0]["schema"].(map[string]any); schema["name"] != "http.ResponseWithMeta{data=[]PayoutListItemResponse,meta=http.ListMeta}" {
		t.Fatalf("schema = %#v", responses[0]["schema"])
	}
}
