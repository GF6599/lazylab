package ui

import (
	"maps"
	"slices"
)

// AsyncCache is a generic map-based cache that tracks loading and error state
// per key. It replaces the repeated (map[K]V, map[K]bool, map[K]error) triplet
// used throughout pipelineViewState.
//
// Each key transitions through a simple state machine:
//
//	idle → SetLoading → (Set | SetErr) → idle
//
// Set clears loading/error, so callers don't need to track transitions manually.
// This is not concurrency-safe; it relies on Bubble Tea's single-goroutine
// Update loop for all mutations.
type AsyncCache[K comparable, V any] struct {
	data    map[K]V
	loading map[K]bool
	err     map[K]error
}

// NewAsyncCache returns an initialized, ready-to-use cache.
func NewAsyncCache[K comparable, V any]() AsyncCache[K, V] {
	return AsyncCache[K, V]{
		data:    make(map[K]V),
		loading: make(map[K]bool),
		err:     make(map[K]error),
	}
}

// Get returns the cached value and whether it exists.
func (c *AsyncCache[K, V]) Get(key K) (V, bool) {
	v, ok := c.data[key]
	return v, ok
}

// Set stores a value and clears loading/error for that key.
func (c *AsyncCache[K, V]) Set(key K, value V) {
	c.data[key] = value
	delete(c.loading, key)
	delete(c.err, key)
}

// SetLoading marks a key as loading and clears its error.
func (c *AsyncCache[K, V]) SetLoading(key K) {
	c.loading[key] = true
	delete(c.err, key)
}

// IsLoading reports whether the key is currently loading.
func (c *AsyncCache[K, V]) IsLoading(key K) bool {
	return c.loading[key]
}

// SetErr stores an error and clears loading for that key.
func (c *AsyncCache[K, V]) SetErr(key K, err error) {
	c.err[key] = err
	delete(c.loading, key)
}

// Err returns the error for a key, or nil.
func (c *AsyncCache[K, V]) Err(key K) error {
	return c.err[key]
}

// Delete removes all state for a key.
func (c *AsyncCache[K, V]) Delete(key K) {
	delete(c.data, key)
	delete(c.loading, key)
	delete(c.err, key)
}

// Clear resets the cache to empty.
func (c *AsyncCache[K, V]) Clear() {
	c.data = make(map[K]V)
	c.loading = make(map[K]bool)
	c.err = make(map[K]error)
}

// Len returns the number of cached values.
func (c *AsyncCache[K, V]) Len() int {
	return len(c.data)
}

// Keys returns all keys that have cached values.
func (c *AsyncCache[K, V]) Keys() []K {
	return slices.Collect(maps.Keys(c.data))
}

// LoadingMap exposes the underlying loading map for backward compatibility
// with shouldFetchPipelineData guards. Prefer IsLoading for new code.
func (c *AsyncCache[K, V]) LoadingMap() map[K]bool {
	return c.loading
}
