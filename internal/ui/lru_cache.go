package ui

// LRUCache is a generic least-recently-used cache with a fixed maximum size.
// When the cache is full, the least recently used entry is evicted to make room
// for new ones. Get promotes an entry to the most recently used position.
//
// This is not concurrency-safe; it relies on Bubble Tea's single-goroutine
// Update loop for all mutations.
type LRUCache[K comparable, V any] struct {
	maxSize int
	data    map[K]V
	order   []K // oldest at front, newest at back
}

// NewLRUCache returns an initialized cache that holds at most maxSize entries.
// A maxSize of zero or negative means no entries will ever be stored.
func NewLRUCache[K comparable, V any](maxSize int) LRUCache[K, V] {
	return LRUCache[K, V]{
		maxSize: maxSize,
		data:    make(map[K]V, maxSize),
		order:   make([]K, 0, maxSize),
	}
}

// Get returns the cached value for key and reports whether it was found.
// A hit promotes the key to the most recently used position.
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	v, ok := c.data[key]
	if ok {
		c.promote(key)
	}
	return v, ok
}

// Set stores a value for key. If the key already exists, its value is updated
// and it is promoted to the most recently used position. If the cache is full,
// the least recently used entry is evicted first.
func (c *LRUCache[K, V]) Set(key K, value V) {
	if c.maxSize <= 0 {
		return
	}

	if _, ok := c.data[key]; ok {
		c.data[key] = value
		c.promote(key)
		return
	}

	// Evict the oldest entry if at capacity.
	if len(c.order) >= c.maxSize {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.data, oldest)
	}

	c.data[key] = value
	c.order = append(c.order, key)
}

// Delete removes a key from the cache.
func (c *LRUCache[K, V]) Delete(key K) {
	if _, ok := c.data[key]; !ok {
		return
	}
	delete(c.data, key)
	c.removeFromOrder(key)
}

// Len returns the number of entries in the cache.
func (c *LRUCache[K, V]) Len() int {
	return len(c.data)
}

// Range iterates over all entries from oldest to newest. If fn returns false,
// iteration stops early.
func (c *LRUCache[K, V]) Range(fn func(K, V) bool) {
	for _, key := range c.order {
		if !fn(key, c.data[key]) {
			return
		}
	}
}

// Clear removes all entries from the cache.
func (c *LRUCache[K, V]) Clear() {
	c.data = make(map[K]V, c.maxSize)
	c.order = c.order[:0]
}

// promote moves key to the back of the order slice (most recently used).
func (c *LRUCache[K, V]) promote(key K) {
	c.removeFromOrder(key)
	c.order = append(c.order, key)
}

// removeFromOrder removes key from the order slice.
func (c *LRUCache[K, V]) removeFromOrder(key K) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
