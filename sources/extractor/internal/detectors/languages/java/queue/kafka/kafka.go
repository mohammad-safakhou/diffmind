// Package kafka detects Java/Kotlin Kafka producer calls.
package kafka

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/java/queue/internal/producer"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "java.queue.kafka" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	out := producer.Detect(idx, []producer.PlatformSpec{{
		Framework: "spring",
		Platform:  "kafka",
		Types: []string{
			"KafkaTemplate",
			"ReplyingKafkaTemplate",
			"RoutingKafkaTemplate",
			"StreamBridge",
		},
		Methods: []string{"send", "sendDefault"},
	}})
	out = append(out, producer.SpringCloudStreamBindings(idx)...)
	return dedupeBindings(out)
}

func dedupeBindings(in []ast.FrameworkBinding) []ast.FrameworkBinding {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, b := range in {
		key := b.Kind + "\x00" + b.Trigger
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, b)
	}
	return out
}
