// Package jms detects Java/Kotlin JMS producer calls.
package jms

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/java/queue/internal/producer"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "java.queue.jms" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	return producer.Detect(idx, []producer.PlatformSpec{{
		Framework: "spring",
		Platform:  "jms",
		Types:     []string{"JmsTemplate"},
		Methods:   []string{"send", "convertAndSend"},
	}})
}
