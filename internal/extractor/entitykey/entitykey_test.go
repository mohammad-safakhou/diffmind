package entitykey

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestSemanticLooseCompatibility(t *testing.T) {
	tests := []struct {
		name string
		item model.BaseEntity
		want string
	}{
		{
			name: "route parameter syntax",
			item: model.BaseEntity{Type: "http_route", Details: map[string]any{"method": "GET", "path": "/orders/:id"}},
			want: "http_route|get|/orders/{}|",
		},
		{
			name: "database resource and platform",
			item: model.BaseEntity{
				Type: "db_operation", Platform: "PostgreSQL",
				Details: map[string]any{"table": "orders", "operation": "SELECT"},
			},
			want: "db_operation|order|read|postgres",
		},
		{
			name: "scheduled job",
			item: model.BaseEntity{Type: "scheduled_job", Name: "ReportJob.run"},
			want: "exposure-job|reportjob.run|",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SemanticLoose(tt.item); got != tt.want {
				t.Fatalf("SemanticLoose() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizers(t *testing.T) {
	if got := QueueDestination("orders-consumer"); got != "orders" {
		t.Fatalf("QueueDestination() = %q", got)
	}
	if got := CanonicalRoutePath("//Orders/<int:id>/"); got != "/orders/{}" {
		t.Fatalf("CanonicalRoutePath() = %q", got)
	}
	if got := NormalizeDBOperation("hardDeleteAll"); got != "delete" {
		t.Fatalf("NormalizeDBOperation() = %q", got)
	}
}
