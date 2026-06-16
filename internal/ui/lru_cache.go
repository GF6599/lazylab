package ui

import "container/list"

// LRUCache is a generic least-recently-used cache with a fixed maximum size.
// When the cache is full, the least recently used entry is evicted to make room
// for new ones. Get promotes an entry to the most recently used position.
//
// All operations are O(1): a doubly-linked list (container/list) tracks order
// from most-recently-used (front) to least-recently-used (back), and a map
// keyed by the user key holds *list.Element references for direct removal and
// promotion without a linear scan.
//
// Get reorders the LRU list as a side effect; use Peek for read-only access
// from render paths so View() never mutates shared state.
//
// This is not concurrency-safe; it relies on Bubble Tea's single-goroutine
// Update loop for all mutations.
type LRUCache[K comparable, V any] struct {
	maxSize int
	data    map[K]*list.Element
	order   *list.List // front = newest, back = oldest
}

// lruEntry is what each list element stores. Keeping the key alongside the
// value lets eviction (which works from the back of the list) delete the
// corresponding map entry without an extra lookup.
type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// NewLRUCache returns an initialized cache that holds at most maxSize entries.
// A maxSize of zero or negative means no entries will ever be stored.
func NewLRUCache[K comparable, V any](maxSize int) LRUCache[K, V] {
	return LRUCache[K, V]{
		maxSize: maxSize,
		data:    make(map[K]*list.Element, maxSize),
		order:   list.New(),
	}
}

// Get returns the cached value for key and reports whether it was found.
// A hit promotes the key to the most recently used position.
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	elem, ok := c.data[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*lruEntry[K, V]).value, true
}

// Peek returns the cached value for key without touching the LRU order. Use
// from render paths (View) where mutation would smuggle Update-style side
// effects into the read-only View contract.
func (c *LRUCache[K, V]) Peek(key K) (V, bool) {
	elem, ok := c.data[key]
	if !ok {
		var zero V
		return zero, false
	}
	return elem.Value.(*lruEntry[K, V]).value, true
}

// Set stores a value for key. If the key already exists, its value is updated
// and it is promoted to the most recently used position. If the cache is full,
// the least recently used entry is evicted first.
func (c *LRUCache[K, V]) Set(key K, value V) {
	if c.maxSize <= 0 {
		return
	}
	if elem, ok := c.data[key]; ok {
		elem.Value.(*lruEntry[K, V]).value = value
		c.order.MoveToFront(elem)
		return
	}
	if c.order.Len() >= c.maxSize {
		c.removeElement(c.order.Back())
	}
	c.data[key] = c.order.PushFront(&lruEntry[K, V]{key: key, value: value})
}

// Delete removes a key from the cache.
func (c *LRUCache[K, V]) Delete(key K) {
	elem, ok := c.data[key]
	if !ok {
		return
	}
	c.removeElement(elem)
}

// Len returns the number of entries in the cache.
func (c *LRUCache[K, V]) Len() int {
	return c.order.Len()
}

// Range iterates over all entries from oldest to newest. If fn returns false,
// iteration stops early. The traversal walks the list back-to-front because
// the back holds the oldest entry under the front-is-newest convention.
func (c *LRUCache[K, V]) Range(fn func(K, V) bool) {
	for e := c.order.Back(); e != nil; e = e.Prev() {
		entry := e.Value.(*lruEntry[K, V])
		if !fn(entry.key, entry.value) {
			return
		}
	}
}

// Clear removes all entries from the cache.
func (c *LRUCache[K, V]) Clear() {
	c.data = make(map[K]*list.Element, c.maxSize)
	c.order.Init()
}

// removeElement deletes elem from both the list and the map. Centralizing this
// keeps the two structures in sync — forgetting either half is the classic LRU
// bug.
func (c *LRUCache[K, V]) removeElement(elem *list.Element) {
	entry := elem.Value.(*lruEntry[K, V])
	c.order.Remove(elem)
	delete(c.data, entry.key)
}
