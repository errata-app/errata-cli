package docstore

import (
	"fmt"
	"sync"
)

// Collection is a thread-safe in-memory document store with secondary indexes.
type Collection struct {
	mu    sync.RWMutex
	docs  map[string]*Document
	index *InvertedIndex
}

// NewCollection creates an empty collection.
func NewCollection() *Collection {
	return &Collection{
		docs:  make(map[string]*Document),
		index: NewInvertedIndex(),
	}
}

// Insert adds a new document. Returns an error if the ID already exists.
func (c *Collection) Insert(doc *Document) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.docs[doc.ID]; exists {
		return fmt.Errorf("document %q already exists", doc.ID)
	}

	stored := doc.Clone()
	c.docs[stored.ID] = stored
	c.index.Add(stored)
	return nil
}

// Get retrieves a document by ID. Returns nil if not found.
func (c *Collection) Get(id string) *Document {
	c.mu.RLock()
	defer c.mu.RUnlock()

	doc, ok := c.docs[id]
	if !ok {
		return nil
	}
	return doc.Clone()
}

// Update modifies fields of an existing document. Returns an error if not found.
func (c *Collection) Update(id string, fields map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	doc, ok := c.docs[id]
	if !ok {
		return fmt.Errorf("document %q not found", id)
	}

	// Apply field updates
	for k, v := range fields {
		doc.Set(k, v)
	}

	// Re-index with current values
	c.index.Remove(doc)
	c.index.Add(doc)

	return nil
}

// Delete removes a document by ID. Returns an error if not found.
func (c *Collection) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	doc, ok := c.docs[id]
	if !ok {
		return fmt.Errorf("document %q not found", id)
	}

	c.index.Remove(doc)
	delete(c.docs, id)
	return nil
}

// Count returns the number of documents in the collection.
func (c *Collection) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.docs)
}

// Find returns all documents matching the given query.
func (c *Collection) Find(q *Query) []*Document {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Optimization: use inverted index for single equality filters.
	if canUseIndex(q) {
		f := q.filters[0]
		ids := c.index.Lookup(f.field, f.value)
		results := make([]*Document, 0, len(ids))
		for _, id := range ids {
			if doc, ok := c.docs[id]; ok {
				results = append(results, doc.Clone())
			}
		}
		return results
	}

	// Full scan with filter matching.
	var results []*Document
	for _, doc := range c.docs {
		if matchesFilters(doc, q.filters) {
			results = append(results, doc.Clone())
		}
	}
	return results
}

func canUseIndex(q *Query) bool {
	return len(q.filters) == 1 && q.filters[0].op == "eq"
}
