package objectives

import "testing"

// TestDefaultOrderAndCount guards the per-objective split: discovery emits jobs
// positionally, so the count and order are part of the contract.
func TestDefaultOrderAndCount(t *testing.T) {
	got := Default()
	wantIDs := []string{
		"exposure.http_route",
		"exposure.webhook",
		"exposure.rpc_endpoint",
		"exposure.queue_consumer",
		"exposure.scheduled_job",
		"exposure.cli_command",
		"dependency.db_operation",
		"dependency.outbound_http",
		"dependency.outbound_rpc",
		"dependency.queue_publish",
		"dependency.command_exec",
		"dependency.cache_operation",
		"dependency.stream_consume",
		// connection_client is appended last (backbone clients, KindClient).
		"client.connection_client",
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("Default() returned %d objectives, want %d", len(got), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("objective[%d].ID = %q, want %q", i, got[i].ID, want)
		}
		if got[i].Type == "" || got[i].DiscoveryPrompt == "" {
			t.Errorf("objective[%d] (%s) missing Type or DiscoveryPrompt", i, got[i].ID)
		}
	}
}

// TestDefaultMetaApplied verifies Default() overlays the example/detail-keys
// from objectiveMeta onto every objective type.
func TestDefaultMetaApplied(t *testing.T) {
	for _, o := range Default() {
		if o.Example == "" {
			t.Errorf("objective %s has no Example (objectiveMeta missing entry?)", o.Type)
		}
		if len(o.DetailKeys) == 0 {
			t.Errorf("objective %s has no DetailKeys", o.Type)
		}
	}
}
