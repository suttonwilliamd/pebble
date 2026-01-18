package storage

import (
	"container/list"
	"sync"
)

// Cache is a two-tier cache for storing frequently accessed objects
type Cache struct {
	memoryCache *list.List
	diskCache   *list.List
	mutex       sync.Mutex
}

// CacheItem represents an item in the cache
type CacheItem struct {
	Key   string
	Value []byte
}

// NewCache creates a new Cache instance
func NewCache() *Cache {
	return &Cache{
		memoryCache: list.New(),
		diskCache:   list.New(),
	}
}

// Get retrieves an item from the cache
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check memory cache
	for e := c.memoryCache.Front(); e != nil; e = e.Next() {
		item := e.Value.(CacheItem)
		if item.Key == key {
			// Move to front (most recently used)
			c.memoryCache.MoveToFront(e)
			return item.Value, true
		}
	}

	// Check disk cache
	for e := c.diskCache.Front(); e != nil; e = e.Next() {
		item := e.Value.(CacheItem)
		if item.Key == key {
			// Move to front (most recently used)
			c.diskCache.MoveToFront(e)
			return item.Value, true
		}
	}

	return nil, false
}

// Set adds an item to the cache
func (c *Cache) Set(key string, value []byte) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Add to memory cache
	c.memoryCache.PushFront(CacheItem{Key: key, Value: value})

	// If memory cache is full, move least recently used items to disk cache
	if c.memoryCache.Len() > 100 {
		e := c.memoryCache.Back()
		if e != nil {
			item := e.Value.(CacheItem)
			c.diskCache.PushFront(item)
			c.memoryCache.Remove(e)
		}
	}

	// If disk cache is full, remove least recently used items
	if c.diskCache.Len() > 1000 {
		e := c.diskCache.Back()
		if e != nil {
			c.diskCache.Remove(e)
		}
	}
}

// Remove removes an item from the cache
func (c *Cache) Remove(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Remove from memory cache
	for e := c.memoryCache.Front(); e != nil; e = e.Next() {
		item := e.Value.(CacheItem)
		if item.Key == key {
			c.memoryCache.Remove(e)
			return
		}
	}

	// Remove from disk cache
	for e := c.diskCache.Front(); e != nil; e = e.Next() {
		item := e.Value.(CacheItem)
		if item.Key == key {
			c.diskCache.Remove(e)
			return
		}
	}
}
