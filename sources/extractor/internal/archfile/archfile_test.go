package archfile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/catalog"
)

const sampleMain = `schema: diffmind.discovery.v1
service: order-service
vars:
  events_queue: order-events
  orders_db: postgres
exposures:
  - type: http_route
    name: POST /v1/orders
    summary: Create an order
    details:
      method: POST
      path: /v1/orders
      auth: "hasRole('USER')"
  - type: queue_consumer
    name: order-events-consumer
    details:
      queue: ${events_queue}
      platform: sqs
      handler: OrderListener.onMessage
dependencies:
  - id: orders_write
    type: db_operation
    name: OrderRepository.save
    details:
      table: orders
      operation: upsert
      platform: ${orders_db}
  - type: outbound_http
    name: GET billing/charge
    details:
      method: GET
      path: /charge
      target_service: billing-service
connections:
  - from: POST /v1/orders
    to: orders_write
    condition: order.valid
  - from: POST /v1/orders
    to: GET billing/charge
`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func importFile(t *testing.T, store *catalog.Store, path string) (catalog.Document, catalog.ImportSummary) {
	t.Helper()
	resolved, err := Resolve(path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	in, err := ToModel(resolved, "file:test")
	if err != nil {
		t.Fatalf("tomodel: %v", err)
	}
	doc, sum, err := store.ImportManual(in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return doc, sum
}

func idSet(doc catalog.Document) []string {
	var ids []string
	for _, e := range doc.Exposures {
		ids = append(ids, e.ID)
	}
	for _, d := range doc.Dependencies {
		ids = append(ids, d.ID)
	}
	for _, c := range doc.Connections {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// TestRoundTripIsNoOp is the gate: author → import → export → re-import must add
// nothing and produce identical durable identities. A failure means file-read
// and run-import disagree on identity.
func TestRoundTripIsNoOp(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFile(t, dir, "diffmind.yaml", sampleMain)

	store1 := catalog.NewStore(filepath.Join(dir, "cat1"))
	doc1, sum1 := importFile(t, store1, mainPath)
	if sum1.Added != 6 { // 2 exposures + 2 deps + 2 connections
		t.Fatalf("expected 6 added on first import, got %+v", sum1)
	}

	// Export the catalog back to a discovery file.
	exported, err := Marshal(Document(doc1))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	exportPath := writeFile(t, dir, "exported.yaml", string(exported))

	// Re-import the export into a fresh catalog: identities must match doc1.
	store2 := catalog.NewStore(filepath.Join(dir, "cat2"))
	doc2, _ := importFile(t, store2, exportPath)

	a, b := idSet(doc1), idSet(doc2)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("identity drift across round trip:\n doc1=%v\n doc2=%v", a, b)
	}

	// Re-importing the export into the ORIGINAL catalog must add nothing.
	_, sum3 := importFile(t, store1, exportPath)
	if sum3.Added != 0 {
		t.Fatalf("re-import added %d records, want 0 (round trip not idempotent): %+v", sum3.Added, sum3)
	}
}

func TestResolveVarsAndIncludes(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "architecture")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: order-service
vars:
  q: order-events
include:
  - ./architecture/more.yaml
exposures:
  - type: queue_consumer
    name: c1
    details:
      queue: ${q}
      platform: sqs
      handler: H.on
`)
	writeFile(t, sub, "more.yaml", `schema: diffmind.discovery.v1
vars:
  q: payments-events
exposures:
  - type: queue_consumer
    name: c2
    details:
      queue: ${q}
      platform: sqs
      handler: H.on
`)

	resolved, err := Resolve(filepath.Join(dir, "diffmind.yaml"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved.Exposures) != 2 {
		t.Fatalf("expected 2 exposures from include, got %d", len(resolved.Exposures))
	}
	got := map[string]string{}
	for _, e := range resolved.Exposures {
		got[e.Name], _ = e.Details["queue"].(string)
	}
	if got["c1"] != "order-events" {
		t.Errorf("c1 queue = %q, want order-events", got["c1"])
	}
	if got["c2"] != "payments-events" { // child var shadows parent
		t.Errorf("c2 queue = %q, want payments-events (child var shadow)", got["c2"])
	}
	// Included file inherits the root service (it set none of its own).
	if resolved.Exposures[1].Service != "order-service" {
		t.Errorf("included entity service = %q, want order-service", resolved.Exposures[1].Service)
	}
}

func TestResourcesRoundTripThroughModel(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: orders
resources:
  - id: orders_db
    kind: datastore
    platform: postgres
    name: Orders DB
    instance: orders
dependencies:
  - type: db_operation
    name: OrderRepository.save
    resource: orders_db
    details: {table: orders, operation: write, platform: postgres}
`)
	resolved, err := Resolve(mainPath)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	graph, err := ToGraph(resolved, "file:test")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if len(graph.Resources) != 1 || graph.Resources[0].ID != "orders_db" {
		t.Fatalf("resources = %+v, want orders_db", graph.Resources)
	}
	if len(graph.Dependencies) != 1 || graph.Dependencies[0].ResourceID != "orders_db" {
		t.Fatalf("dependency resource = %+v, want orders_db", graph.Dependencies)
	}

	store := catalog.NewStore(filepath.Join(dir, "cat"))
	doc, _ := importFile(t, store, mainPath)
	exported, err := Marshal(Document(doc))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	exportPath := writeFile(t, dir, "exported.yaml", string(exported))
	exportedResolved, err := Resolve(exportPath)
	if err != nil {
		t.Fatalf("resolve exported: %v", err)
	}
	exportedGraph, err := ToGraph(exportedResolved, "file:exported")
	if err != nil {
		t.Fatalf("graph exported: %v", err)
	}
	if exportedGraph.Dependencies[0].ResourceID != "orders_db" {
		t.Fatalf("exported dependency resource = %q, want orders_db", exportedGraph.Dependencies[0].ResourceID)
	}
}

func TestDraftPromotesDerivedResourceWithoutWritingMain(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: orders
dependencies:
  - type: db_operation
    name: OrderRepository.save
    details: {table: orders, operation: write, platform: postgres}
`)
	before, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := ToGraph(resolved, "file:test")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Resources) != 1 || !graph.Resources[0].Derived {
		t.Fatalf("expected one derived resource, got %+v", graph.Resources)
	}
	draft, err := Draft(mainPath, EditSet{Resources: []ResourceEdit{{
		ID:   graph.Resources[0].ID,
		Name: strPtr("Orders Database"),
	}}})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if draft.Summary.Resources != 1 {
		t.Fatalf("draft summary = %+v, want one resource edit", draft.Summary)
	}
	if !strings.Contains(draft.YAML, "Orders Database") {
		t.Fatalf("draft yaml missing resource edit:\n%s", draft.YAML)
	}
	after, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("draft wrote main file:\nbefore=%s\nafter=%s", before, after)
	}
}

func strPtr(s string) *string { return &s }

// An undeclared ${var} is left verbatim, not an error: run-discovered facts
// carry config placeholders (UUIDs, env names) that are not authoring vars, and
// erroring would break the whole graph/merge for one literal. Declared vars
// still expand (covered by TestResolveVars).
func TestUnknownVariablePreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: s
exposures:
  - type: http_route
    name: GET /x
    details:
      method: GET
      path: ${missing}
`)
	resolved, err := Resolve(filepath.Join(dir, "diffmind.yaml"))
	if err != nil {
		t.Fatalf("undeclared var should pass through, got error: %v", err)
	}
	if path, _ := resolved.Exposures[0].Details["path"].(string); path != "${missing}" {
		t.Errorf("undeclared var should be verbatim, got %q", path)
	}
}

// A connection whose endpoint isn't present is dropped, not fatal — otherwise a
// single dangling edge (e.g. left behind when a run-imported dependency collapses
// onto an already-present fact and is skipped) would blank the whole graph.
func TestDanglingConnectionDropped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: s
exposures:
  - id: e1
    type: http_route
    name: GET /x
    details: {method: GET, path: /x}
dependencies:
  - id: d1
    type: db_operation
    name: read t
    details: {table: t, operation: read, platform: postgres}
connections:
  - {from: e1, to: d1}
  - {from: e1, to: ghost}
`)
	resolved, err := Resolve(filepath.Join(dir, "diffmind.yaml"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	g, err := ToGraph(resolved, "test")
	if err != nil {
		t.Fatalf("ToGraph must tolerate a dangling connection, got: %v", err)
	}
	if len(g.Connections) != 1 {
		t.Fatalf("dangling connection should be dropped, kept %d", len(g.Connections))
	}
}

func TestEnvPlaceholderPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: s
dependencies:
  - type: db_operation
    name: r
    details: {table: t, operation: read, platform: postgres, url: "${DB_URL:localhost}"}
`)
	resolved, err := Resolve(filepath.Join(dir, "diffmind.yaml"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	url, _ := resolved.Dependencies[0].Details["url"].(string)
	if url != "${DB_URL:localhost}" {
		t.Errorf("runtime placeholder mangled: %q", url)
	}
}

func TestIncludeCycleDetected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "schema: diffmind.discovery.v1\ninclude: [./b.yaml]\n")
	writeFile(t, dir, "b.yaml", "schema: diffmind.discovery.v1\ninclude: [./a.yaml]\n")
	_, err := Resolve(filepath.Join(dir, "a.yaml"))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected include cycle error, got: %v", err)
	}
}

// TestMergeNonClobber verifies MergeIntoMain appends only genuinely new entries,
// preserves human comments/vars, and is a no-op on a second run.
func TestMergeNonClobber(t *testing.T) {
	dir := t.TempDir()
	mainBody := `schema: diffmind.discovery.v1
service: order-service
# a human comment that must survive
vars:
  q: order-events
exposures:
  - type: http_route
    name: POST /v1/orders
    details: {method: POST, path: /v1/orders}
`
	mainPath := writeFile(t, dir, "diffmind.yaml", mainBody)
	genPath := filepath.Join(dir, ".diffmind.generated.yaml")

	// Generated proposes one duplicate (same identity) and one new entity.
	writeFile(t, dir, ".diffmind.generated.yaml", `schema: diffmind.discovery.v1
service: order-service
exposures:
  - type: http_route
    name: POST /v1/orders
    details: {method: POST, path: /v1/orders}
dependencies:
  - type: db_operation
    name: OrderRepository.save
    details: {table: orders, operation: upsert, platform: postgres}
`)

	appended, err := MergeIntoMain(mainPath, genPath)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if appended != 1 {
		t.Fatalf("expected 1 appended (the new dependency), got %d", appended)
	}
	merged, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(merged)
	if !strings.Contains(text, "a human comment that must survive") {
		t.Error("human comment was clobbered")
	}
	if !strings.Contains(text, "${q}") && !strings.Contains(text, "q: order-events") {
		t.Error("vars block was clobbered")
	}
	if !strings.Contains(text, "OrderRepository.save") {
		t.Error("new dependency was not appended")
	}
	if _, err := os.Stat(genPath); !os.IsNotExist(err) {
		t.Error("generated file should be consumed after merge")
	}

	// Second merge with the same proposal is a no-op (file already merged in).
	writeFile(t, dir, ".diffmind.generated.yaml", `schema: diffmind.discovery.v1
service: order-service
dependencies:
  - type: db_operation
    name: OrderRepository.save
    details: {table: orders, operation: upsert, platform: postgres}
`)
	appended2, err := MergeIntoMain(mainPath, genPath)
	if err != nil {
		t.Fatalf("merge 2: %v", err)
	}
	if appended2 != 0 {
		t.Fatalf("second merge appended %d, want 0", appended2)
	}
}

// TestMergePreviewMatchesApply guards the compute/apply split: the previewed
// append count must equal what MergeIntoMain actually writes.
func TestMergePreviewMatchesApply(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFile(t, dir, "diffmind.yaml", `schema: diffmind.discovery.v1
service: order-service
exposures:
  - type: http_route
    name: POST /v1/orders
    details: {method: POST, path: /v1/orders}
`)
	genPath := filepath.Join(dir, ".diffmind.generated.yaml")
	writeFile(t, dir, ".diffmind.generated.yaml", `schema: diffmind.discovery.v1
service: order-service
exposures:
  - type: http_route
    name: POST /v1/orders
    details: {method: POST, path: /v1/orders}
dependencies:
  - type: db_operation
    name: OrderRepository.save
    details: {table: orders, operation: upsert, platform: postgres}
`)

	plan, err := MergePreview(mainPath, genPath)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(plan.Append) != 1 || len(plan.Skip) != 1 {
		t.Fatalf("preview: append=%d skip=%d, want 1/1", len(plan.Append), len(plan.Skip))
	}
	// Generated file must be untouched by a preview.
	if _, err := os.Stat(genPath); err != nil {
		t.Fatal("preview must not consume the generated file")
	}
	applied, err := MergeIntoMain(mainPath, genPath)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied != len(plan.Append) {
		t.Fatalf("apply count %d != preview append count %d", applied, len(plan.Append))
	}
}

func TestValidateRejectsBadYAML(t *testing.T) {
	if err := Validate([]byte("schema: diffmind.discovery.v1\nexposures: [\n")); err == nil {
		t.Fatal("expected parse error for malformed YAML")
	}
	if err := Validate([]byte("schema: wrong.schema\n")); err == nil {
		t.Fatal("expected error for unsupported schema")
	}
	if err := Validate([]byte("schema: diffmind.discovery.v1\nservice: s\n")); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
}

// TestStatusRoundTrip verifies curation status/source survive resolve and a
// no-op parse→marshal, and that empty status resolves as verified semantics.
func TestStatusRoundTrip(t *testing.T) {
	dir := t.TempDir()
	body := `schema: diffmind.discovery.v1
service: orders
exposures:
  - type: http_route
    name: POST /v1/orders
    details: {method: POST, path: /v1/orders}
  - type: http_route
    name: GET /v1/orders
    status: proposed
    source: run:abc
    details: {method: GET, path: /v1/orders}
`
	p := writeFile(t, dir, "diffmind.yaml", body)
	resolved, err := Resolve(p)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Exposures[0].Status != "" {
		t.Fatalf("authored fact should have empty (verified) status, got %q", resolved.Exposures[0].Status)
	}
	if resolved.Exposures[1].Status != "proposed" || resolved.Exposures[1].Source != "run:abc" {
		t.Fatalf("proposed fact lost status/source: %+v", resolved.Exposures[1])
	}
	g, err := ToGraph(resolved, "test")
	if err != nil {
		t.Fatalf("tograph: %v", err)
	}
	if g.Coverage.Verified != 1 || g.Coverage.Proposed != 1 || g.Coverage.Total != 2 {
		t.Fatalf("coverage = %+v, want 1 verified / 1 proposed / 2 total", g.Coverage)
	}
}

// TestDraftAcceptAndReject checks the review primitives: a status edit flips a
// proposed fact to verified, and a delete removes a fact plus its connections.
func TestDraftAcceptAndReject(t *testing.T) {
	dir := t.TempDir()
	body := `schema: diffmind.discovery.v1
service: orders
exposures:
  - type: http_route
    name: POST /v1/orders
    status: proposed
    details: {method: POST, path: /v1/orders}
dependencies:
  - type: db_operation
    name: OrderRepository.save
    status: proposed
    details: {table: orders, operation: write, platform: postgres}
connections:
  - from: POST /v1/orders
    to: OrderRepository.save
    status: proposed
`
	p := writeFile(t, dir, "diffmind.yaml", body)

	verified := "verified"
	manual := "manual"
	accept, err := Draft(p, EditSet{
		Exposures: []EntityEdit{{ID: "POST /v1/orders", Status: &verified, Source: &manual}},
	})
	if err != nil {
		t.Fatalf("accept draft: %v", err)
	}
	if !strings.Contains(accept.YAML, "status: verified") {
		t.Fatalf("accept did not write verified status:\n%s", accept.YAML)
	}

	reject, err := Draft(p, EditSet{
		Delete: []DeleteRef{{Kind: "dependency", ID: "OrderRepository.save"}},
	})
	if err != nil {
		t.Fatalf("reject draft: %v", err)
	}
	if strings.Contains(reject.YAML, "OrderRepository.save") {
		t.Fatalf("reject did not remove the dependency:\n%s", reject.YAML)
	}
	if strings.Contains(reject.YAML, "POST /v1/orders\n    to: OrderRepository.save") || countConns(reject.YAML) != 0 {
		t.Fatalf("reject left a dangling connection:\n%s", reject.YAML)
	}
}

func countConns(yaml string) int {
	// crude: count "  - from:" lines under connections in the draft
	n := 0
	for _, line := range strings.Split(yaml, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- from:") {
			n++
		}
	}
	return n
}
