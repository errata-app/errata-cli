package docstore

import "fmt"

// Document represents a stored document with an ID and arbitrary string fields.
type Document struct {
	ID     string
	Fields map[string]string
}

// NewDocument creates a document with the given ID and fields.
func NewDocument(id string, fields map[string]string) *Document {
	f := make(map[string]string, len(fields))
	for k, v := range fields {
		f[k] = v
	}
	return &Document{ID: id, Fields: f}
}

// Get returns the value of a field, or empty string if not set.
func (d *Document) Get(key string) string {
	if d.Fields == nil {
		return ""
	}
	return d.Fields[key]
}

// Set sets a field value.
func (d *Document) Set(key, value string) {
	if d.Fields == nil {
		d.Fields = make(map[string]string)
	}
	d.Fields[key] = value
}

// Clone returns a deep copy of the document.
func (d *Document) Clone() *Document {
	return NewDocument(d.ID, d.Fields)
}

// String returns a readable representation.
func (d *Document) String() string {
	return fmt.Sprintf("Document{ID: %q, Fields: %v}", d.ID, d.Fields)
}
