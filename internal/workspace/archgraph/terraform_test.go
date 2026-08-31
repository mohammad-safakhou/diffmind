package archgraph

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTF(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanTerraformSubscriptions(t *testing.T) {
	repo := t.TempDir()
	writeTF(t, filepath.Join(repo, "modules", "tracking"), "events.tf", `
resource "aws_sqs_queue" "campaign_events_sqs" {
  name = "external_tracking_catalogue_campaign_events_sqs"

  visibility_timeout_seconds = 600
}

resource "aws_sns_topic_subscription" "campaign_events_subscription" {
  topic_arn            = var.aws_sns_topic-catalogue_campaign_sns-arn
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.campaign_events_sqs.arn
  raw_message_delivery = true
}
`)
	writeTF(t, filepath.Join(repo, "modules", "billing"), "billing.tf", `
resource "aws_sns_topic" "billing_events" {
  name = "billing-events-sns"
}

resource "aws_sqs_queue" "billing_worker" {
  name = "billing-worker-sqs"
}

resource "aws_sns_topic_subscription" "billing_sub" {
  topic_arn = aws_sns_topic.billing_events.arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.billing_worker.arn
}

resource "aws_sns_topic_subscription" "email_sub" {
  topic_arn = aws_sns_topic.billing_events.arn
  protocol  = "email"
  endpoint  = "ops@example.com"
}

resource "aws_sns_topic_subscription" "unresolvable" {
  topic_arn = module.other.topic_arn
  protocol  = "sqs"
  endpoint  = module.other.queue_arn
}
`)

	subs := ScanTerraformSubscriptions(repo)
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d: %+v", len(subs), subs)
	}
	if subs[0].Topic != "billing-events-sns" || subs[0].Queue != "billing-worker-sqs" {
		t.Fatalf("unexpected first subscription: %+v", subs[0])
	}
	if subs[1].Topic != "catalogue_campaign_sns" || subs[1].Queue != "external_tracking_catalogue_campaign_events_sqs" {
		t.Fatalf("unexpected second subscription: %+v", subs[1])
	}
}

// SNS fan-out: publisher → topic → (terraform subscription) → queue → consumer
// must traverse the extra queue-to-queue hop.
func TestBuildFlowViewFollowsQueueSubscription(t *testing.T) {
	graph := &ArchGraph{
		RunID: "run-sub",
		Services: []*ServiceNode{
			{
				Name:       "producer-api",
				Known:      true,
				HTTPRoutes: []EntitySummary{{ID: "http.entry", Kind: "http_endpoint", Name: "POST /campaigns"}},
				Connections: []ConnectionSummary{{
					FromID:       "http.entry",
					FromName:     "POST /campaigns",
					FromType:     "http_endpoint",
					ToID:         "queuepub.campaign",
					ToName:       "publish campaign events",
					ToType:       "queue_publish",
					FlowID:       "flow.entry",
					EntrypointID: "http.entry",
					Kind:         "queue_publish",
				}},
			},
			{
				Name:           "consumer-worker",
				Known:          true,
				QueueConsumers: []EntitySummary{{ID: "queue.campaign", Kind: "queue_consumer", Name: "campaign-events-sqs", Details: map[string]any{"queue": "campaign-events-sqs"}}},
			},
		},
		QueueNodes: []*QueueNode{
			{ID: "campaignsns", Name: "campaign-sns", Kind: "sns"},
			{ID: "campaigneventssqs", Name: "campaign-events-sqs", Kind: "sqs"},
		},
		Edges: []*GraphEdge{
			{
				From: "producer-api", To: "queue:campaignsns", Type: "queue_publish", Label: "publish",
				Details: []EntitySummary{{ID: "queuepub.campaign", Kind: "queue_publish", Name: "publish campaign events", Details: map[string]any{"topic": "campaign-sns"}}},
			},
			{From: "queue:campaignsns", To: "queue:campaigneventssqs", Type: "queue_subscription", Label: "sns→sqs"},
			{From: "queue:campaigneventssqs", To: "consumer-worker", Type: "queue_consume", Label: "consume"},
		},
	}

	view, ok := BuildFlowView(graph, "producer-api", "http.entry", FlowOptions{Depth: 6, MaxNodes: 100})
	if !ok {
		t.Fatalf("expected flow view")
	}
	foundSub, foundConsumer := false, false
	for _, e := range view.Edges {
		if e.Kind == "queue_subscription" {
			foundSub = true
			if !e.Async {
				t.Errorf("subscription hop should be async")
			}
		}
		if e.Kind == "queue_consume" {
			foundConsumer = true
		}
	}
	if !foundSub {
		t.Fatalf("flow must include the queue_subscription hop; edges: %+v", view.Edges)
	}
	if !foundConsumer {
		t.Fatalf("flow must reach the consumer through the subscribed queue; edges: %+v", view.Edges)
	}
	var names []string
	for _, s := range view.Services {
		names = append(names, s.Name)
	}
	found := false
	for _, n := range names {
		if n == "consumer-worker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("consumer-worker service missing from flow; services: %v", names)
	}
}
