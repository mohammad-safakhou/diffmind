package rabbitmq

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestDetectRabbitProducerExchangeAndRoutingKey(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "rabbit",
					CalleeRaw:   "rabbit.convertAndSend",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{
						{Index: 0, Source: `"orders"`, Kind: "literal"},
						{Index: 1, Source: `"created"`, Kind: "literal"},
						{Index: 2, Source: "payload", Kind: "identifier"},
					},
				}},
				FieldTypes: map[string]string{"com.example.Publisher.rabbit": "RabbitTemplate"},
			},
		},
		FieldTypes: map[string]string{"com.example.Publisher.rabbit": "RabbitTemplate"},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "rabbitmq: orders.created" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}
