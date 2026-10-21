// Package server is the HTTP/WebSocket side of the host: web client, pairing API and signaling.
package server

import (
	"slices"
	"sync"
	"time"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/pairing"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

// Context is the shared, thread-safe state of the HTTP and signaling handlers.
type Context struct {
	Pairing    *pairing.Manager
	Hub        *Hub
	AppVersion string
	HostName   string
	// LocalPage, if set, renders the host's status page at /host for loopback clients only.
	LocalPage func() []byte

	mu       sync.Mutex
	settings settings.Settings
}

func NewContext(s settings.Settings, p *pairing.Manager, appVersion, hostName string) *Context {
	return &Context{Pairing: p, Hub: NewHub(), AppVersion: appVersion, HostName: hostName, settings: s.Sanitized()}
}

func (c *Context) Settings() settings.Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

func (c *Context) SetSettings(s settings.Settings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings = s.Sanitized()
}

type ConnectedClient struct {
	ID            string
	DisplayIndex  int
	RemoteAddress string
	ConnectedAt   time.Time
	LatencyMs     *int
	Stats         *protocol.ClientStats
}

type hubEntry struct {
	info ConnectedClient
	kick func(reason string)
}

// Hub tracks live sessions; at most one per display (a new connection replaces the old one).
type Hub struct {
	mu      sync.Mutex
	entries map[string]*hubEntry
	// OnChange is called with the current client list after every change, outside the lock.
	OnChange func([]ConnectedClient)
}

func NewHub() *Hub { return &Hub{entries: map[string]*hubEntry{}} }

func (h *Hub) Clients() []ConnectedClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clientsLocked()
}

func (h *Hub) clientsLocked() []ConnectedClient {
	out := make([]ConnectedClient, 0, len(h.entries))
	for _, e := range h.entries {
		out = append(out, e.info)
	}
	slices.SortFunc(out, func(a, b ConnectedClient) int { return a.DisplayIndex - b.DisplayIndex })
	return out
}

func (h *Hub) IsInUse(display int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.info.DisplayIndex == display {
			return true
		}
	}
	return false
}

// Register adds a session and returns the number of live sessions afterwards. A previous session
// on the same display is kicked with reason "replaced".
func (h *Hub) Register(client ConnectedClient, kick func(reason string)) int {
	h.mu.Lock()
	var evicted []*hubEntry
	for id, e := range h.entries {
		if e.info.DisplayIndex == client.DisplayIndex && id != client.ID {
			evicted = append(evicted, e)
			delete(h.entries, id)
		}
	}
	h.entries[client.ID] = &hubEntry{info: client, kick: kick}
	total := len(h.entries)
	h.mu.Unlock()
	for _, e := range evicted {
		e.kick("replaced")
	}
	h.notify()
	return total
}

func (h *Hub) Unregister(id string) {
	h.mu.Lock()
	_, ok := h.entries[id]
	delete(h.entries, id)
	h.mu.Unlock()
	if ok {
		h.notify()
	}
}

func (h *Hub) Update(id string, latencyMs *int, stats *protocol.ClientStats) {
	h.mu.Lock()
	e, ok := h.entries[id]
	if ok {
		if latencyMs != nil {
			e.info.LatencyMs = latencyMs
		}
		if stats != nil {
			e.info.Stats = stats
		}
	}
	h.mu.Unlock()
	if ok {
		h.notify()
	}
}

func (h *Hub) DisconnectAll(reason string) {
	h.mu.Lock()
	all := make([]*hubEntry, 0, len(h.entries))
	for _, e := range h.entries {
		all = append(all, e)
	}
	h.entries = map[string]*hubEntry{}
	h.mu.Unlock()
	for _, e := range all {
		e.kick(reason)
	}
	h.notify()
}

func (h *Hub) notify() {
	if h.OnChange != nil {
		h.OnChange(h.Clients())
	}
}
