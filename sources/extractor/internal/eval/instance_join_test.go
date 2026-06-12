package eval

import (
	"context"
	"path/filepath"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/floor"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// The downstream company graph joins "service A talks to service B" on the
// concrete instance both sides reference. This test is that contract end to
// end: two independent repos wiring the same SQS queue must emit the SAME
// instance identity from their separate extractions — logical name, platform,
// and URL template.
func TestSharedQueueInstanceIdentityJoins(t *testing.T) {
	producer := floorEntities(t, "sqs-producer")
	consumer := floorEntities(t, "sqs-consumer")

	var pub, con *model.BaseEntity
	for i := range producer.Dependencies {
		if producer.Dependencies[i].Type == "queue_publish" {
			pub = &producer.Dependencies[i].BaseEntity
		}
	}
	for i := range consumer.Exposures {
		if consumer.Exposures[i].Type == "queue_consumer" {
			con = &consumer.Exposures[i].BaseEntity
		}
	}
	if pub == nil || con == nil {
		t.Fatalf("floor must find both sides: publish=%v consume=%v", pub != nil, con != nil)
	}

	if pub.Instance == "" || pub.Instance != con.Instance {
		t.Errorf("instances must join: producer %q vs consumer %q", pub.Instance, con.Instance)
	}
	if pub.Platform != con.Platform {
		t.Errorf("platforms must join: producer %q vs consumer %q", pub.Platform, con.Platform)
	}
	if pub.InstanceRef == nil || con.InstanceRef == nil {
		t.Fatalf("both sides need an InstanceRef: producer=%v consumer=%v", pub.InstanceRef, con.InstanceRef)
	}
	if pub.InstanceRef.URLTemplate == "" || pub.InstanceRef.URLTemplate != con.InstanceRef.URLTemplate {
		t.Errorf("URL templates must join: producer %q vs consumer %q",
			pub.InstanceRef.URLTemplate, con.InstanceRef.URLTemplate)
	}
	if pub.InstanceRef.Kind != "sqs" || con.InstanceRef.Kind != "sqs" {
		t.Errorf("both refs should be sqs, got %q / %q", pub.InstanceRef.Kind, con.InstanceRef.Kind)
	}
}

func floorEntities(t *testing.T, fixture string) Extracted {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "eval", fixture))
	if err != nil {
		t.Fatal(err)
	}
	set, err := LoadExpected(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	repo := set.ResolvedRepoPath()
	idx, err := astpkg.Build(context.Background(), repo, "", cfg.Runtime.Workers, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := floor.Run(context.Background(), idx, repo, cfg)
	return Extracted{Exposures: res.Exposures, Dependencies: res.Dependencies, Connections: res.Connections}
}
