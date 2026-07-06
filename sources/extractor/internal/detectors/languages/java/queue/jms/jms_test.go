package jms

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestDetectJMSProducerDestination(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "jms",
					CalleeRaw:   "jms.convertAndSend",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{
						{Index: 0, Source: `"order-events"`, Kind: "literal"},
						{Index: 1, Source: "payload", Kind: "identifier"},
					},
				}},
				LocalTypes: map[string]string{"com.example.Publisher.publish.jms": "JmsTemplate"},
			},
		},
		LocalTypes: map[string]string{"com.example.Publisher.publish.jms": "JmsTemplate"},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "jms: order-events" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}
