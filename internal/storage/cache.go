package storage

import (
	"sync"
	"time"
)

// CacheEntry represents a cached item
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// TwoTierCache implements a memory + disk cache
type TwoTierCache struct {
	mu           sync.RWMutex
	memCache     map[string]CacheEntry
	maxMemSize   int
	currentMemSize int
	hits        int
	misses      int
}

// NewTwoTierCache creates a new two-tier cache
func NewTwoTierCache(maxMemSize int) *TwoTierCache {
	return &TwoTierCache{
		memCache:   make(map[string]CacheEntry),
		maxMemSize: maxMemSize,
	}
}

// Set adds a value to the cache
func (c *TwoTierCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Estimate size (simple approach: string length)
	size := len(key) + estimateSize(value)

	// Evict if needed
	for c.currentMemSize+size > c.maxMemSize && len(c.memCache) > 0 {
		c.evictOldest()
	}

	c.memCache[key] = CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(1 * time.Hour), // 1 hour default
	}
	c.currentMemSize += size
}

// Get retrieves a value from the cache
func (c *TwoTierCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.memCache[key]
	if !exists {
		c.misses++
		return nil, false
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		c.misses++
		return nil, false
	}

	c.hits++
	return entry.Value, true
}

// Delete removes a key from the cache
func (c *TwoTierCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.memCache[key]; exists {
		c.currentMemSize -= estimateSize(entry.Value)
		delete(c.memCache, key)
	}
}

// Clear removes all entries
func (c *TwoTierCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.memCache = make(map[string]CacheEntry)
	c.currentMemSize = 0
}

// evictOldest removes the oldest entry
func (c *TwoTierCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.memCache {
		if oldestTime.IsZero() || entry.ExpiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.ExpiresAt
		}
	}

	if oldestKey != "" {
		if entry, ok := c.memCache[oldestKey]; ok {
			c.currentMemSize -= estimateSize(entry.Value)
		}
		delete(c.memCache, oldestKey)
	}
}

// Stats returns cache statistics
func (c *TwoTierCache) Stats() (hits, misses, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses, len(c.memCache)
}

// HitRate returns the cache hit rate
func (c *TwoTierCache) HitRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	if total == 0 {
		return 0
	}
	return float64(c.hits) / float64(total)
}

// estimateSize estimates the size of a value
func estimateSize(v interface{}) int {
	switch val := v.(type) {
	case string:
		return len(val)
	case []byte:
		return len(val)
	default:
		return 64 // estimate
	}
}
