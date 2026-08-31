package sqs

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func TestDetectSQSProducerByReceiverTypeAndBuilderDestination(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "sqsClient",
					CalleeRaw:   "sqsClient.sendMessage",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{{
						Index:  0,
						Source: `SendMessageRequest.builder().queueUrl("https://sqs.eu-central-1.amazonaws.com/123/orders").messageBody(payload).build()`,
						Kind:   "call",
					}},
				}},
				FieldTypes: map[string]string{"com.example.Publisher.sqsClient": "software.amazon.awssdk.services.sqs.SqsClient"},
			},
		},
		FieldTypes: map[string]string{"com.example.Publisher.sqsClient": "software.amazon.awssdk.services.sqs.SqsClient"},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Kind != "queue_publisher" || got[0].Trigger != "sqs: https://sqs.eu-central-1.amazonaws.com/123/orders" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}

func TestDetectSNSProducerByReceiverTypeAndBuilderDestination(t *testing.T) {
	idx := &ast.ProjectIndex{
		Files: map[string]*ast.FileAST{
			"Publisher.java": {
				Language: "java",
				Calls: []ast.CallSite{{
					Caller:      "com.example.Publisher.publish",
					ReceiverRaw: "sns",
					CalleeRaw:   "sns.publish",
					File:        "Publisher.java",
					Arguments: []ast.ArgumentExpr{{
						Index:  0,
						Source: `PublishRequest.builder().topicArn("arn:aws:sns:eu-central-1:123:campaign-events").message(payload).build()`,
						Kind:   "call",
					}},
				}},
				LocalTypes: map[string]string{"com.example.Publisher.publish.sns": "SnsClient"},
			},
		},
		LocalTypes: map[string]string{"com.example.Publisher.publish.sns": "SnsClient"},
	}
	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("expected one binding, got %+v", got)
	}
	if got[0].Trigger != "sns: arn:aws:sns:eu-central-1:123:campaign-events" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}
