// Package rabbitmq detects Java/Kotlin RabbitMQ producer calls.
package rabbitmq

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/java/queue/internal/producer"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "java.queue.rabbitmq" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	return producer.Detect(idx, []producer.PlatformSpec{{
		Framework: "spring",
		Platform:  "rabbitmq",
		Types:     []string{"RabbitTemplate", "AmqpTemplate"},
		Methods:   []string{"send", "convertAndSend"},
	}})
}
