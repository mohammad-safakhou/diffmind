// Package sqs detects Java/Kotlin SQS and SNS producer calls.
package sqs

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/internal/producer"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "java.queue.sqs" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	return producer.Detect(idx, []producer.PlatformSpec{
		{
			Framework: "spring",
			Platform:  "sqs",
			Types:     []string{"SqsTemplate", "SqsClient", "AmazonSQS"},
			Methods:   []string{"send", "sendMany", "sendMessage", "sendMessageBatch"},
		},
		{
			Framework: "spring",
			Platform:  "sns",
			Types:     []string{"SnsTemplate", "SnsClient", "AmazonSNS"},
			Methods:   []string{"send", "publish"},
		},
	})
}
