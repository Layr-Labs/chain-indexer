package contractRegistry

import (
	"strings"
	"sync"
	"sync/atomic"
)

type InMemoryContractRegistry struct {
	contracts atomic.Value
	writeMu   sync.Mutex
}

func NewInMemoryContractRegistry() *InMemoryContractRegistry {
	r := &InMemoryContractRegistry{}
	r.contracts.Store([]string{})
	return r
}

func (r *InMemoryContractRegistry) RegisterContracts(addresses []string) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	current := r.contracts.Load().([]string)
	set := make(map[string]struct{}, len(current))
	for _, addr := range current {
		set[addr] = struct{}{}
	}
	for _, addr := range addresses {
		set[strings.ToLower(addr)] = struct{}{}
	}
	merged := make([]string, 0, len(set))
	for addr := range set {
		merged = append(merged, addr)
	}
	r.contracts.Store(merged)
}

func (r *InMemoryContractRegistry) UnregisterContracts(addresses []string) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	remove := make(map[string]struct{}, len(addresses))
	for _, addr := range addresses {
		remove[strings.ToLower(addr)] = struct{}{}
	}
	current := r.contracts.Load().([]string)
	filtered := make([]string, 0, len(current))
	for _, addr := range current {
		if _, ok := remove[addr]; !ok {
			filtered = append(filtered, addr)
		}
	}
	r.contracts.Store(filtered)
}

func (r *InMemoryContractRegistry) ListContracts() []string {
	current := r.contracts.Load().([]string)
	out := make([]string, len(current))
	copy(out, current)
	return out
}
