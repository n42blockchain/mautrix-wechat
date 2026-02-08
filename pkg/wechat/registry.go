package wechat

import (
	"fmt"
	"sort"
	"sync"
)

// ProviderFactory is a function that creates a new Provider instance.
type ProviderFactory func() Provider

// Registry manages provider registration and instantiation.
// Providers register themselves during init() and the bridge core
// selects the appropriate provider based on configuration.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
	}
}

// DefaultRegistry is the global provider registry.
var DefaultRegistry = NewRegistry()

// Register adds a provider factory to the registry.
func (r *Registry) Register(name string, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}
	r.factories[name] = factory
	return nil
}

// Create instantiates a provider by name.
func (r *Registry) Create(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.factories[name]
	if !exists {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	return factory(), nil
}

// List returns the names of all registered providers sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Has returns whether a provider with the given name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.factories[name]
	return exists
}

// Register is a convenience function that registers with the default registry.
func Register(name string, factory ProviderFactory) error {
	return DefaultRegistry.Register(name, factory)
}
