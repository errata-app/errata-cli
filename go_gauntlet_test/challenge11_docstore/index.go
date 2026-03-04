package docstore

// InvertedIndex maps field:value pairs to sets of document IDs for fast lookups.
type InvertedIndex struct {
	entries map[string]map[string]bool
}

// NewInvertedIndex creates an empty inverted index.
func NewInvertedIndex() *InvertedIndex {
	return &InvertedIndex{entries: make(map[string]map[string]bool)}
}

func indexKey(field, value string) string {
	return field + "\x00" + value
}

// Add indexes all fields of a document.
func (idx *InvertedIndex) Add(doc *Document) {
	for field, value := range doc.Fields {
		key := indexKey(field, value)
		if idx.entries[key] == nil {
			idx.entries[key] = make(map[string]bool)
		}
		idx.entries[key][doc.ID] = true
	}
}

// Remove removes all index entries for a document based on its current field values.
func (idx *InvertedIndex) Remove(doc *Document) {
	for field, value := range doc.Fields {
		key := indexKey(field, value)
		if ids, ok := idx.entries[key]; ok {
			delete(ids, doc.ID)
			if len(ids) == 0 {
				delete(idx.entries, key)
			}
		}
	}
}

// Lookup returns all document IDs that have the given field:value pair.
func (idx *InvertedIndex) Lookup(field, value string) []string {
	key := indexKey(field, value)
	ids, ok := idx.entries[key]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}
