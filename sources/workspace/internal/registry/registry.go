// Package registry maintains the in-memory service registry that
// aggregates DiffMind architecture data and extracted identities.
package registry

import (
	"fmt"
	"sync"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// ServiceEntry holds all known data about a single service.
type ServiceEntry struct {
	Name         string
	RepoPath     string
	Architecture *model.ServiceArchitecture
	Identity     *model.ServiceIdentity
}

// Registry is a thread-safe in-memory store of service data.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*ServiceEntry // keyed by service name
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{services: make(map[string]*ServiceEntry)}
}

// AddArchitecture registers or updates a service's architecture data.
func (r *Registry) AddArchitecture(name string, arch *model.ServiceArchitecture) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.getOrCreate(name)
	entry.Architecture = arch
	entry.RepoPath = arch.RepoPath
	arch.ServiceName = name
}

// AddIdentity registers or updates a service's identity.
func (r *Registry) AddIdentity(name string, id *model.ServiceIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.getOrCreate(name)
	entry.Identity = id
}

// Get returns a service entry or nil.
func (r *Registry) Get(name string) *ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.services[name]
}

// All returns all registered services.
func (r *Registry) All() []*ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]*ServiceEntry, 0, len(r.services))
	for _, e := range r.services {
		entries = append(entries, e)
	}
	return entries
}

// AllWithArchitecture returns services that have architecture data.
func (r *Registry) AllWithArchitecture() []*ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var entries []*ServiceEntry
	for _, e := range r.services {
		if e.Architecture != nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// Summary returns a human-readable summary.
func (r *Registry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := len(r.services)
	withArch := 0
	withID := 0
	for _, e := range r.services {
		if e.Architecture != nil {
			withArch++
		}
		if e.Identity != nil {
			withID++
		}
	}
	return fmt.Sprintf("%d services (%d with architecture, %d with identity)", total, withArch, withID)
}

func (r *Registry) getOrCreate(name string) *ServiceEntry {
	e, ok := r.services[name]
	if !ok {
		e = &ServiceEntry{Name: name}
		r.services[name] = e
	}
	return e
}
