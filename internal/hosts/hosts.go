package hosts

import "sync"

type Registry struct {
	mu      sync.RWMutex
	schemes map[string]string
}

func NewRegistry(initial []string) *Registry {
	registry := &Registry{schemes: make(map[string]string)}
	for _, host := range initial {
		registry.Allow(host, "https")
	}
	return registry
}

func (r *Registry) Allow(host, scheme string) {
	if host == "" {
		return
	}
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	r.mu.Lock()
	r.schemes[host] = scheme
	r.mu.Unlock()
}

func (r *Registry) Lookup(host string) (string, bool) {
	r.mu.RLock()
	scheme, ok := r.schemes[host]
	r.mu.RUnlock()
	if !ok {
		return "", false
	}
	if scheme == "" {
		scheme = "https"
	}
	return scheme, true
}
