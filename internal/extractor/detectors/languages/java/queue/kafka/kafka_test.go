package kafka

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func TestDetectKafkaTemplateProducerByFieldType(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "template",
					CalleeRaw:   "template.send",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{
						{Index: 0, Source: `"campaign-events"`, Kind: "literal"},
						{Index: 1, Source: "payload", Kind: "identifier"},
					},
				}},
				FieldTypes: map[string]string{"com.example.Publisher.template": "KafkaTemplate<String, Event>"},
			},
		},
		FieldTypes: map[string]string{"com.example.Publisher.template": "KafkaTemplate<String, Event>"},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Kind != "queue_publisher" || got[0].Trigger != "kafka: campaign-events" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}

func TestDetectKafkaSendDefaultUsesDefaultTopicConfig(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "template",
					CalleeRaw:   "template.sendDefault",
					File:        "Publisher.java",
					Arguments:   []ast.ArgumentExpr{{Index: 0, Source: "payload", Kind: "identifier"}},
				}},
				LocalTypes: map[string]string{"com.example.Publisher.publish.template": "org.springframework.kafka.core.KafkaTemplate<java.lang.String, com.example.Event>"},
			},
		},
		LocalTypes: map[string]string{"com.example.Publisher.publish.template": "org.springframework.kafka.core.KafkaTemplate<java.lang.String, com.example.Event>"},
		Configs: map[string]*ast.ConfigFile{"application.yml": {Entries: []ast.ConfigEntry{
			{Key: "spring.kafka.template.default-topic", Value: "default-topic"},
		}}},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "kafka: default-topic" {
		t.Fatalf("trigger = %q, want kafka default topic", got[0].Trigger)
	}
}

func TestDetectStreamBridgeProducerResolvesBindingDestination(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "bridge",
					CalleeRaw:   "bridge.send",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{
						{Index: 0, Source: `"campaign-out-0"`, Kind: "literal"},
						{Index: 1, Source: "payload", Kind: "identifier"},
					},
				}},
				LocalTypes: map[string]string{"com.example.Publisher.publish.bridge": "StreamBridge"},
			},
		},
		LocalTypes: map[string]string{"com.example.Publisher.publish.bridge": "StreamBridge"},
		Configs: map[string]*ast.ConfigFile{"application.yml": {Entries: []ast.ConfigEntry{
			{Key: "spring.cloud.stream.bindings.campaign-out-0.destination", Value: "campaign-events"},
		}}},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "kafka: campaign-events" {
		t.Fatalf("trigger = %q, want stream destination", got[0].Trigger)
	}
}

func TestDetectSpringCloudStreamOutputBindingFromConfig(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{},
		Configs: map[string]*ast.ConfigFile{"application.yml": {
			Path: "application.yml",
			Entries: []ast.ConfigEntry{
				{Key: "spring.cloud.stream.bindings.order-out-0.destination", Value: "order-events", Line: 12},
			},
		}},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "kafka: order-events" || got[0].File != "application.yml" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}
