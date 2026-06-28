package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestYAMLAndJSONRoundTrip(t *testing.T) {
	body := `
schema: diffmind.service.v1
service:
  id: gateway-service
  name: gateway-service
objects:
  http_endpoints:
    - id: http.create_campaign
      kind: http_endpoint
      name: Create campaign
      method: POST
      path: /campaigns
      status: confirmed
      confidence: high
      origin: deterministic
observations:
  - id: obs.http.create_campaign.route
    object_ref: http.create_campaign
    perspective: route_registration
evidence:
  - id: ev.http.create_campaign.route
    type: source_location
flows:
  - id: flow.create_campaign
    kind: request_flow
    entrypoint: http.create_campaign
    nodes:
      - id: n1
        ref: http.create_campaign
    status: confirmed
    confidence: high
    origin: deterministic
`
	doc, err := DecodeYAML(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeYAML: %v", err)
	}
	doc.Objects.HTTPEndpoints[0].Observations = []string{"obs.http.create_campaign.route"}
	doc.Objects.HTTPEndpoints[0].EvidenceRefs = []string{"ev.http.create_campaign.route"}
	var buf bytes.Buffer
	if err := EncodeJSON(&buf, doc); err != nil {
		t.Fatalf("EncodeJSON: %v", err)
	}
	if _, err := DecodeJSON(&buf); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
}

func TestRejectLineNumberIdentity(t *testing.T) {
	doc := &Document{
		Schema:  SchemaServiceV1,
		Service: Service{ID: "svc", Name: "svc"},
		Objects: Objects{HTTPEndpoints: []HTTPEndpoint{{
			ObjectiveBase: ObjectiveBase{
				ID: "routes_go_line_42", Kind: "http_endpoint", Name: "bad",
				Status: StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
			},
		}}},
	}
	if err := Validate(doc); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRejectUnknownFlowNode(t *testing.T) {
	doc := &Document{
		Schema:  SchemaServiceV1,
		Service: Service{ID: "svc", Name: "svc"},
		Objects: Objects{HTTPEndpoints: []HTTPEndpoint{{
			ObjectiveBase: ObjectiveBase{
				ID: "http.create", Kind: "http_endpoint", Name: "create",
				Status: StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
			},
		}}},
		Flows: []Flow{{ID: "flow.create", Nodes: []FlowNode{{ID: "n1", Ref: "http.create"}}, Edges: []FlowEdge{{From: "n1", To: "n2"}}}},
	}
	if err := Validate(doc); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateCanonicalRequiresGeneratedShape(t *testing.T) {
	doc := &Document{
		Schema:  SchemaServiceV1,
		Service: Service{ID: "svc", Name: "svc"},
		Objects: Objects{HTTPEndpoints: []HTTPEndpoint{{
			ObjectiveBase: ObjectiveBase{
				ID: "http.create", Kind: "http_endpoint", Name: "create",
				Status: StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
			},
		}}},
	}
	if err := Validate(doc); err != nil {
		t.Fatalf("Validate should allow minimal semantic document: %v", err)
	}
	if err := ValidateCanonical(doc); err == nil {
		t.Fatal("expected canonical validation error")
	}

	doc.Flows = []Flow{{
		ID:         "flow.create",
		Kind:       "request_flow",
		Entrypoint: "http.create",
		Nodes:      []FlowNode{{ID: "n1", Ref: "http.create", Role: "entrypoint"}},
		Status:     StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
		EvidenceRefs: []string{"ev.http.create.1"},
	}}
	doc.Observations = []Observation{{ID: "obs.http.create.1", ObjectRef: "http.create", Perspective: "route_registration"}}
	doc.Evidence = []Evidence{{ID: "ev.http.create.1", Type: "source_location", Source: "code"}}
	doc.Objects.HTTPCalls = []HTTPCall{}
	doc.Objects.DBResources = []DBResource{}
	doc.Objects.DBQueries = []DBQuery{}
	doc.Objects.QueueConsumers = []QueueConsumer{}
	doc.Objects.QueuePublishers = []QueuePublisher{}
	doc.Objects.RPCEndpoints = []RPCObjective{}
	doc.Objects.RPCCalls = []RPCObjective{}
	doc.Objects.CLICommands = []CLICommand{}
	doc.Objects.Activations = []Activation{}
	doc.Objects.CacheOperations = []CacheOperation{}
	doc.Objects.ConfigReads = []ConfigRead{}
	doc.Objects.FeatureFlags = []FeatureFlag{}
	doc.Objects.HTTPEndpoints[0].Observations = []string{"obs.http.create.1"}
	doc.Objects.HTTPEndpoints[0].EvidenceRefs = []string{"ev.http.create.1"}
	if err := ValidateCanonical(doc); err != nil {
		t.Fatalf("ValidateCanonical: %v", err)
	}

	doc.Objects.HTTPEndpoints[0].Origin = OriginLLM
	if err := Validate(doc); err != nil {
		t.Fatalf("Validate should still allow protocol origin llm: %v", err)
	}
	if err := ValidateCanonical(doc); err == nil {
		t.Fatal("expected canonical validation to reject origin llm")
	}
	doc.Objects.HTTPEndpoints[0].Origin = OriginDeterministic

	doc.Flows = []Flow{{ID: "flow.create", Kind: "request_flow", From: "http.create", To: "http.create", Origin: OriginLLM}}
	if err := ValidateCanonical(doc); err == nil {
		t.Fatal("expected canonical validation to reject flow origin llm")
	}
	doc.Flows = []Flow{{
		ID:         "flow.create",
		Kind:       "request_flow",
		Entrypoint: "http.create",
		Nodes:      []FlowNode{{ID: "n1", Ref: "http.create", Role: "entrypoint"}},
		Status:     StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
		EvidenceRefs: []string{"ev.http.create.1"},
	}}

	doc.Evidence[0].Source = "llm"
	if err := ValidateCanonical(doc); err == nil {
		t.Fatal("expected canonical validation to reject llm evidence source")
	}
}

func TestValidateCanonicalRequiresEntrypointFlows(t *testing.T) {
	doc := canonicalDoc()
	doc.Flows = []Flow{}
	if err := ValidateCanonical(doc); err == nil {
		t.Fatal("expected canonical validation to reject entrypoint without flow")
	}
}

func TestValidateRejectsUnknownDataDependencyRefs(t *testing.T) {
	doc := canonicalDoc()
	doc.Flows[0].DataDependencies = []DataDependency{{
		ID:   "data.bad",
		From: DataEndpoint{ObjectRef: "http.create", Expression: "request.body.id"},
		To:   DataEndpoint{ObjectRef: "dbq.missing", Expression: "campaigns.id"},
		Kind: "value_flow", Confidence: ConfidenceHigh,
	}}
	if err := Validate(doc); err == nil {
		t.Fatal("expected validation to reject unknown data dependency object ref")
	}
}

func canonicalDoc() *Document {
	return &Document{
		Schema:  SchemaServiceV1,
		Service: Service{ID: "svc", Name: "svc"},
		Objects: Objects{
			HTTPEndpoints: []HTTPEndpoint{{
				ObjectiveBase: ObjectiveBase{
					ID: "http.create", Kind: "http_endpoint", Name: "create",
					Status: StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
					Observations: []string{"obs.http.create.1"}, EvidenceRefs: []string{"ev.http.create.1"},
				},
				Method: "POST", Path: "/create",
			}},
			HTTPCalls:       []HTTPCall{},
			DBResources:     []DBResource{},
			DBQueries:       []DBQuery{},
			QueueConsumers:  []QueueConsumer{},
			QueuePublishers: []QueuePublisher{},
			RPCEndpoints:    []RPCObjective{},
			RPCCalls:        []RPCObjective{},
			CLICommands:     []CLICommand{},
			Activations:     []Activation{},
			CacheOperations: []CacheOperation{},
			ConfigReads:     []ConfigRead{},
			FeatureFlags:    []FeatureFlag{},
		},
		Observations: []Observation{{ID: "obs.http.create.1", ObjectRef: "http.create", Perspective: "route_registration"}},
		Evidence:     []Evidence{{ID: "ev.http.create.1", Type: "source_location", Source: "code", Confidence: ConfidenceHigh}},
		Flows: []Flow{{
			ID:         "flow.create",
			Kind:       "request_flow",
			Entrypoint: "http.create",
			Nodes:      []FlowNode{{ID: "n1", Ref: "http.create", Role: "entrypoint"}},
			Status:     StatusConfirmed, Confidence: ConfidenceHigh, Origin: OriginDeterministic,
			EvidenceRefs: []string{"ev.http.create.1"},
		}},
	}
}
