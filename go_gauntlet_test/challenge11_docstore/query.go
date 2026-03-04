package docstore

import "strings"

// Query represents a composable document query with filter conditions.
type Query struct {
	filters []filter
}

type filter struct {
	field string
	op    string
	value string
}

// Where creates a new query with a single filter condition.
// Supported operators: eq, neq, gt, lt, gte, lte, contains.
func Where(field, op, value string) *Query {
	return &Query{
		filters: []filter{{field: field, op: op, value: value}},
	}
}

// And adds an additional filter condition (logical AND).
func (q *Query) And(field, op, value string) *Query {
	q.filters = append(q.filters, filter{field: field, op: op, value: value})
	return q
}

// matchesFilters returns true if the document satisfies all filter conditions.
func matchesFilters(doc *Document, filters []filter) bool {
	for _, f := range filters {
		if matchFilter(doc, f) {
			return true
		}
	}
	return false
}

// matchFilter checks whether a document satisfies a single filter condition.
func matchFilter(doc *Document, f filter) bool {
	val := doc.Get(f.field)

	switch f.op {
	case "eq":
		return val == f.value
	case "neq":
		return val != f.value
	case "gt":
		return compareValues(f.value, val) > 0
	case "lt":
		return compareValues(f.value, val) < 0
	case "gte":
		return compareValues(val, f.value) >= 0
	case "lte":
		return compareValues(val, f.value) <= 0
	case "contains":
		return strings.Contains(val, f.value)
	default:
		return false
	}
}

// compareValues performs lexicographic comparison of two strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareValues(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
