package tether

import (
	"maps"
	"sync"
)

// Presence tracks per-session metadata and makes it available to all
// sessions. It handles the common pattern of knowing who is here and
// what they are doing.
//
// Use Presence for collaborative features: who is viewing a card, who
// is typing, which page each user is on, or any per-session state
// that other sessions need to see.
//
//	var viewers = tether.NewPresence[ViewInfo]()
//
//	// In Handle - set when state changes:
//	viewers.Set(sess.ID(), ViewInfo{Card: id, Name: s.Name})
//
//	// In Handle - clear when leaving:
//	viewers.Clear(sess.ID())
//
//	// In OnDisconnect - clean up:
//	viewers.Clear(sess.ID())
//
//	// In Render - read all:
//	viewers.Each(s.SessionID, func(sid string, v ViewInfo) {
//	    // show presence for other sessions
//	})
type Presence[T any] struct {
	mu      sync.RWMutex
	entries map[string]T // sessionID -> metadata
}

// NewPresence creates an empty presence tracker.
func NewPresence[T any]() *Presence[T] {
	return &Presence[T]{entries: make(map[string]T)}
}

// Set stores metadata for a session. Call this in Handle when the
// session's state changes (e.g. they open a card, start typing).
func (p *Presence[T]) Set(sessionID string, val T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[sessionID] = val
}

// Clear removes a session's metadata. Call in Handle when the user
// navigates away, and in OnDisconnect for cleanup.
func (p *Presence[T]) Clear(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, sessionID)
}

// Get returns the metadata for a session, if present.
func (p *Presence[T]) Get(sessionID string) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.entries[sessionID]
	return v, ok
}

// All returns a snapshot of all session metadata. The returned map
// is a copy - mutations do not affect the presence state.
func (p *Presence[T]) All() map[string]T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]T, len(p.entries))
	maps.Copy(out, p.entries)
	return out
}

// Len returns the number of tracked sessions.
func (p *Presence[T]) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

// Each calls fn for every tracked session, excluding the given
// session ID (pass empty to include everyone). Use this in Render
// to show what other users are doing without including yourself.
func (p *Presence[T]) Each(exclude string, fn func(sessionID string, val T)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for sid, v := range p.entries {
		if sid != exclude {
			fn(sid, v)
		}
	}
}
