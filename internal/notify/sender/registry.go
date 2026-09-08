package sender

import (
	"sync"
)

var (
	registryMu sync.RWMutex
	senders    = make(map[string]Sender)
)

// Register registers a Sender driver for a specific channel kind.
func Register(s Sender) {
	if s == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	senders[s.Kind()] = s
}

// Get retrieves the Sender driver registered for the given kind.
func Get(kind string) (Sender, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	s, ok := senders[kind]
	return s, ok
}

// RegisteredKinds returns all currently registered channel kinds.
func RegisteredKinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	kinds := make([]string, 0, len(senders))
	for k := range senders {
		kinds = append(kinds, k)
	}
	return kinds
}
