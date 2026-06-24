package proxemby

import "sync"

type HostRegistry struct {
	mu      sync.RWMutex
	schemes map[string]string
}

func NewHostRegistry(initial []string) *HostRegistry {
	registry := &HostRegistry{schemes: make(map[string]string)}
	for _, host := range initial {
		registry.Allow(host, "https")
	}
	return registry
}

func (r *HostRegistry) Allow(host, scheme string) {
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

func (r *HostRegistry) Lookup(host string) (string, bool) {
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
