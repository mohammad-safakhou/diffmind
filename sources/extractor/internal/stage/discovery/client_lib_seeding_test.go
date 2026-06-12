package discovery

import (
	"fmt"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// S2: objectives without framework detectors must still accumulate sharding
// evidence from client-library usage (imports +2, call receivers/callees +1),
// instead of falling back to one whole-repo "find ALL X" call.
func TestObjectiveCandidateWeightsClientLibSeeding(t *testing.T) {
	idx := &astpkg.ProjectIndex{
		Files: map[string]*astpkg.FileAST{
			"src/pub/OrderPublisher.java": {
				Path:    "src/pub/OrderPublisher.java",
				Imports: []astpkg.ImportDecl{{Path: "software.amazon.awssdk.services.sqs.SqsClient"}},
				Calls: []astpkg.CallSite{
					{ReceiverRaw: "sqsClient", CalleeRaw: "sendMessage", File: "src/pub/OrderPublisher.java"},
				},
			},
			"src/util/Strings.java": {
				Path:  "src/util/Strings.java",
				Calls: []astpkg.CallSite{{ReceiverRaw: "s", CalleeRaw: "trim", File: "src/util/Strings.java"}},
			},
		},
	}
	weights := objectiveCandidateWeights(idx, objByType(t, "queue_publish"), "")
	// import (+2) + sqsClient receiver (+1)
	if got := weights["src/pub/OrderPublisher.java"]; got != 3 {
		t.Errorf("publisher file should weigh 3, got %d (all: %v)", got, weights)
	}
	if _, ok := weights["src/util/Strings.java"]; ok {
		t.Errorf("one-letter receiver must not match a client lib: %v", weights)
	}
}

// Enough seeded weight must actually trigger sharding for a detector-less
// objective.
func TestPlanShardsFromSeededWeights(t *testing.T) {
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{}}
	for i := 0; i < 30; i++ {
		path := fmt.Sprintf("src/pub/Publisher%02d.java", i)
		idx.Files[path] = &astpkg.FileAST{
			Path:    path,
			Imports: []astpkg.ImportDecl{{Path: "org.springframework.kafka.core.KafkaTemplate"}},
		}
	}
	shards := PlanShards(idx, objByType(t, "queue_publish"), "")
	if len(shards) < 2 {
		t.Fatalf("30 seeded files (weight 60) should shard, got %d shards", len(shards))
	}
	seen := map[string]bool{}
	for _, s := range shards {
		for _, f := range s.Files {
			if seen[f] {
				t.Errorf("file %s appears in two shards", f)
			}
			seen[f] = true
		}
	}
	if len(seen) != 30 {
		t.Errorf("shards must partition all candidate files, covered %d/30", len(seen))
	}
}
