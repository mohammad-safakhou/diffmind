package archgraph

import "testing"

func TestBuildFlowViewWalksHTTPQueueAndStopsCycle(t *testing.T) {
	graph := &ArchGraph{
		RunID: "run-flow",
		Services: []*ServiceNode{
			{
				Name:       "entry-api",
				Team:       "alpha",
				HTTPRoutes: []EntitySummary{{ID: "http.entry", Kind: "http_endpoint", Name: "GET /entry"}},
				Connections: []ConnectionSummary{{
					FromID:       "http.entry",
					FromName:     "GET /entry",
					FromType:     "http_endpoint",
					ToID:         "httpcall.worker",
					ToName:       "Call worker",
					ToType:       "http_call",
					FlowID:       "flow.entry",
					EntrypointID: "http.entry",
					Kind:         "http",
					Reachability: "must",
				}},
			},
			{
				Name:       "worker-api",
				Team:       "beta",
				HTTPRoutes: []EntitySummary{{ID: "http.worker", Kind: "http_endpoint", Name: "GET /worker"}},
				Connections: []ConnectionSummary{{
					FromID:       "http.worker",
					FromName:     "GET /worker",
					FromType:     "http_endpoint",
					ToID:         "queuepub.jobs",
					ToName:       "Publish jobs",
					ToType:       "queue_publish",
					FlowID:       "flow.worker",
					EntrypointID: "http.worker",
					Kind:         "queue_publish",
				}},
			},
			{
				Name:           "job-consumer",
				Team:           "gamma",
				QueueConsumers: []EntitySummary{{ID: "queue.jobs", Kind: "queue_consumer", Name: "jobs", Details: map[string]any{"queue": "jobs"}}},
				HTTPRoutes:     []EntitySummary{{ID: "http.consumer", Kind: "http_endpoint", Name: "GET /consumer"}},
				Connections: []ConnectionSummary{{
					FromID:       "queue.jobs",
					FromName:     "jobs",
					FromType:     "queue_consumer",
					ToID:         "httpcall.entry",
					ToName:       "Call entry",
					ToType:       "http_call",
					FlowID:       "flow.consumer",
					EntrypointID: "queue.jobs",
					Kind:         "http",
				}},
			},
		},
		QueueNodes: []*QueueNode{{ID: "jobs", Name: "jobs", Kind: "kafka"}},
		ResourceNodes: []*ResourceNode{{
			ID:       "jobs",
			GraphID:  "queue:jobs",
			Name:     "jobs",
			Kind:     "queue_topic_stream",
			Platform: "kafka",
		}},
		Edges: []*GraphEdge{
			{
				From: "entry-api", To: "worker-api", Type: "http", Label: "HTTP",
				Details: []EntitySummary{{ID: "httpcall.worker", Kind: "http_call", Name: "Call worker", Details: map[string]any{"method": "GET", "path": "/worker"}}},
			},
			{
				From: "worker-api", To: "queue:jobs", Type: "queue_publish", Label: "publish",
				Details: []EntitySummary{{ID: "queuepub.jobs", Kind: "queue_publish", Name: "Publish jobs", Details: map[string]any{"topic": "jobs"}}},
			},
			{From: "queue:jobs", To: "job-consumer", Type: "queue_consume", Label: "consume"},
			{
				From: "job-consumer", To: "entry-api", Type: "http", Label: "HTTP",
				Details: []EntitySummary{{ID: "httpcall.entry", Kind: "http_call", Name: "Call entry", Details: map[string]any{"method": "GET", "path": "/entry"}}},
			},
		},
	}

	view, ok := BuildFlowView(graph, "entry-api", "http.entry", FlowOptions{Depth: 6, MaxNodes: 100})
	if !ok {
		t.Fatal("expected flow view")
	}
	if view.Status != "complete" {
		t.Fatalf("expected complete flow, got %s quality=%v", view.Status, view.Quality)
	}
	if view.Stats.ServiceCount != 3 {
		t.Fatalf("expected three services, got %+v", view.Services)
	}
	if view.Stats.CycleCount != 1 {
		t.Fatalf("expected one cycle, got stats %+v edges=%+v", view.Stats, view.Edges)
	}
	if !flowHasEdge(view, "http", "exact_exposure", false) {
		t.Fatalf("expected exact HTTP exposure edge, got %+v", view.Edges)
	}
	if !flowHasEdge(view, "queue_consume", "exact_queue_consumer", true) {
		t.Fatalf("expected exact async queue consumer edge, got %+v", view.Edges)
	}
	if !flowHasCycle(view) {
		t.Fatalf("expected cycle edge, got %+v", view.Edges)
	}
}

func TestBuildFlowViewTruncatesByMaxNodes(t *testing.T) {
	graph := &ArchGraph{
		RunID: "run-flow",
		Services: []*ServiceNode{{
			Name:       "entry-api",
			HTTPRoutes: []EntitySummary{{ID: "http.entry", Kind: "http_endpoint", Name: "GET /entry"}},
			Connections: []ConnectionSummary{{
				FromID:   "http.entry",
				FromName: "GET /entry",
				FromType: "http_endpoint",
				ToID:     "cache.read",
				ToName:   "Read cache",
				ToType:   "cache_operation",
				Kind:     "cache",
			}},
		}},
	}

	view, ok := BuildFlowView(graph, "entry-api", "http.entry", FlowOptions{Depth: 2, MaxNodes: 1})
	if !ok {
		t.Fatal("expected flow view")
	}
	if view.Status != "truncated" || view.Stats.TruncatedAt != 1 {
		t.Fatalf("expected truncated flow, got status=%s stats=%+v", view.Status, view.Stats)
	}
}

func flowHasEdge(view *FlowView, kind, status string, async bool) bool {
	for _, edge := range view.Edges {
		if edge.Kind == kind && edge.MatchStatus == status && edge.Async == async {
			return true
		}
	}
	return false
}

func flowHasCycle(view *FlowView) bool {
	for _, edge := range view.Edges {
		if edge.Cycle {
			return true
		}
	}
	return false
}
