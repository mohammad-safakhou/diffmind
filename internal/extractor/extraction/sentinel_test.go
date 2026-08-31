package extraction

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/objectives"
)

func TestIsNoResultSentinel(t *testing.T) {
	obj := objectiveForSentinelTest(t, "stream_consume")
	tests := []struct {
		name string
		item Candidate
		want bool
	}{
		{name: "none", item: Candidate{Name: "__none__", Summary: "No stream consumers were found."}, want: true},
		{name: "placeholder", item: Candidate{Name: "placeholder", Summary: "No stream consumers were found."}, want: true},
		{name: "objective id", item: Candidate{Name: obj.ID, Summary: "No stream consumers were found."}, want: true},
		{name: "encoded absence", item: Candidate{Name: "__no_stream_consumers_found__", Summary: "No stream consumers were found."}, want: true},
		{name: "noop with negative summary", item: Candidate{Name: "noop", Summary: "No confirmed external command execution was found."}, want: true},
		{name: "real queue", item: Candidate{Name: "orders-stream", Summary: "Consumes order updates."}, want: false},
		{name: "real noop command", item: Candidate{Name: "noop", Summary: "Executes the noop binary as a health probe."}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNoResultSentinel(obj, tc.item); got != tc.want {
				t.Fatalf("IsNoResultSentinel() = %v, want %v", got, tc.want)
			}
		})
	}
}

func objectiveForSentinelTest(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	for _, obj := range objectives.Default() {
		if obj.Type == typ {
			return obj
		}
	}
	t.Fatalf("objective %q not found", typ)
	return objectives.Objective{}
}
