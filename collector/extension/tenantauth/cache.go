// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth // import "github.com/sag-solutions/otel-fleet/collector/extension/tenantauth"

import (
	"container/list"
	"sync"
	"time"
)

// cacheEntry is a validation result keyed by SHA-256(presented key).
type cacheEntry struct {
	valid bool
	data  *authData // set only when valid

	// freshUntil: served without contacting the control plane before this.
	freshUntil time.Time
	// staleUntil: positive entries may still be served until this time when
	// the control plane is unreachable (stale_if_error).
	staleUntil time.Time
}

// keyCache is a mutex-guarded LRU keyed by the hex SHA-256 of the API key.
type keyCache struct {
	mu         sync.Mutex
	maxEntries int
	ll         *list.List // front = most recently used
	items      map[string]*list.Element
}

type lruItem struct {
	key   string
	entry cacheEntry
}

func newKeyCache(maxEntries int) *keyCache {
	return &keyCache{
		maxEntries: maxEntries,
		ll:         list.New(),
		items:      make(map[string]*list.Element),
	}
}

// get returns the entry for the hashed key, if present (fresh or stale).
func (c *keyCache) get(hash string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[hash]
	if !ok {
		return cacheEntry{}, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*lruItem).entry, true
}

func (c *keyCache) put(hash string, e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[hash]; ok {
		el.Value.(*lruItem).entry = e
		c.ll.MoveToFront(el)
		return
	}
	c.items[hash] = c.ll.PushFront(&lruItem{key: hash, entry: e})
	for len(c.items) > c.maxEntries {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*lruItem).key)
	}
}

func (c *keyCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
