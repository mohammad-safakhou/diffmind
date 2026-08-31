package registry

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
)

func TestRegistry_AddAndGet(t *testing.T) {
	reg := New()

	arch := &model.ServiceArchitecture{
		ServiceName: "order-service",
		RepoPath:    "/repos/order-service",
		Exposures: []model.Exposure{
			{BaseEntity: model.BaseEntity{ID: "e1", Name: "POST /orders"}},
		},
		Dependencies: []model.Dependency{
			{BaseEntity: model.BaseEntity{ID: "d1", Name: "BillingClient"}},
		},
	}
	reg.AddArchitecture("order-service", arch)

	entry := reg.Get("order-service")
	if entry == nil {
		t.Fatal("expected entry for order-service")
	}
	if entry.Architecture == nil {
		t.Fatal("expected architecture")
	}
	if len(entry.Architecture.Exposures) != 1 {
		t.Errorf("expected 1 exposure, got %d", len(entry.Architecture.Exposures))
	}
}

func TestRegistry_AddIdentity(t *testing.T) {
	reg := New()

	reg.AddArchitecture("order-service", &model.ServiceArchitecture{
		ServiceName: "order-service",
		RepoPath:    "/repos/order-service",
	})

	identity := &model.ServiceIdentity{
		ServiceName: "order-service",
		Aliases: []model.IdentityAlias{
			{Kind: "dns", Value: "order-service.example.global"},
			{Kind: "iam_role", Value: "order-service-prod"},
		},
	}
	reg.AddIdentity("order-service", identity)

	entry := reg.Get("order-service")
	if entry.Identity == nil {
		t.Fatal("expected identity")
	}
	if len(entry.Identity.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(entry.Identity.Aliases))
	}
}

func TestRegistry_All(t *testing.T) {
	reg := New()
	reg.AddArchitecture("svc-a", &model.ServiceArchitecture{ServiceName: "svc-a", RepoPath: "/a"})
	reg.AddArchitecture("svc-b", &model.ServiceArchitecture{ServiceName: "svc-b", RepoPath: "/b"})

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("expected 2 entries, got %d", len(all))
	}
}

func TestRegistry_AllWithArchitecture(t *testing.T) {
	reg := New()
	reg.AddArchitecture("svc-a", &model.ServiceArchitecture{ServiceName: "svc-a", RepoPath: "/a"})
	reg.AddIdentity("svc-b", &model.ServiceIdentity{ServiceName: "svc-b"})

	withArch := reg.AllWithArchitecture()
	if len(withArch) != 1 {
		t.Errorf("expected 1 entry with architecture, got %d", len(withArch))
	}
}

func TestRegistry_Summary(t *testing.T) {
	reg := New()
	reg.AddArchitecture("svc-a", &model.ServiceArchitecture{ServiceName: "svc-a", RepoPath: "/a"})
	reg.AddIdentity("svc-a", &model.ServiceIdentity{ServiceName: "svc-a"})
	reg.AddArchitecture("svc-b", &model.ServiceArchitecture{ServiceName: "svc-b", RepoPath: "/b"})

	summary := reg.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}
