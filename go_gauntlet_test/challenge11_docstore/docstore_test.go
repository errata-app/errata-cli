package docstore

import (
	"fmt"
	"sort"
	"testing"
)

func TestInsertAndGet(t *testing.T) {
	c := NewCollection()
	doc := NewDocument("1", map[string]string{"name": "Alice", "age": "30"})
	if err := c.Insert(doc); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got := c.Get("1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Get("name") != "Alice" {
		t.Errorf("name = %q, want %q", got.Get("name"), "Alice")
	}
	if got.Get("age") != "30" {
		t.Errorf("age = %q, want %q", got.Get("age"), "30")
	}
}

func TestInsertDuplicate(t *testing.T) {
	c := NewCollection()
	doc := NewDocument("1", map[string]string{"name": "Alice"})
	if err := c.Insert(doc); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if err := c.Insert(doc); err == nil {
		t.Fatal("expected error on duplicate insert")
	}
}

func TestDelete(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"name": "Alice"}))
	c.Insert(NewDocument("2", map[string]string{"name": "Bob"}))

	if err := c.Delete("1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if c.Count() != 1 {
		t.Errorf("Count after delete = %d, want 1", c.Count())
	}
	if c.Get("1") != nil {
		t.Error("Get after Delete should return nil")
	}
}

func TestCount(t *testing.T) {
	c := NewCollection()
	if c.Count() != 0 {
		t.Errorf("empty Count = %d, want 0", c.Count())
	}
	c.Insert(NewDocument("1", map[string]string{"x": "1"}))
	c.Insert(NewDocument("2", map[string]string{"x": "2"}))
	if c.Count() != 2 {
		t.Errorf("Count = %d, want 2", c.Count())
	}
}

func TestFindEq(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"name": "Alice", "city": "NYC"}))
	c.Insert(NewDocument("2", map[string]string{"name": "Bob", "city": "LA"}))
	c.Insert(NewDocument("3", map[string]string{"name": "Charlie", "city": "NYC"}))

	results := c.Find(Where("city", "eq", "NYC"))
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	names := docNames(results)
	sort.Strings(names)
	if names[0] != "Alice" || names[1] != "Charlie" {
		t.Errorf("names = %v, want [Alice Charlie]", names)
	}
}

func TestFindWithIndex(t *testing.T) {
	c := NewCollection()
	for i := 0; i < 50; i++ {
		city := "other"
		if i < 5 {
			city = "target"
		}
		c.Insert(NewDocument(fmt.Sprintf("d%d", i), map[string]string{"city": city}))
	}

	results := c.Find(Where("city", "eq", "target"))
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}
}

func TestFindContains(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"name": "Alice Johnson"}))
	c.Insert(NewDocument("2", map[string]string{"name": "Bob Smith"}))
	c.Insert(NewDocument("3", map[string]string{"name": "Alice Williams"}))

	results := c.Find(Where("name", "contains", "Alice"))
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestFindGreaterThan(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"score": "30"}))
	c.Insert(NewDocument("2", map[string]string{"score": "50"}))
	c.Insert(NewDocument("3", map[string]string{"score": "10"}))

	results := c.Find(Where("score", "gt", "25"))
	if len(results) != 2 {
		t.Fatalf("gt 25: got %d results, want 2", len(results))
	}
	scores := docField(results, "score")
	sort.Strings(scores)
	if scores[0] != "30" || scores[1] != "50" {
		t.Errorf("scores = %v, want [30 50]", scores)
	}
}

func TestFindLessThan(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"score": "30"}))
	c.Insert(NewDocument("2", map[string]string{"score": "50"}))
	c.Insert(NewDocument("3", map[string]string{"score": "10"}))

	results := c.Find(Where("score", "lt", "35"))
	if len(results) != 2 {
		t.Fatalf("lt 35: got %d results, want 2", len(results))
	}
	scores := docField(results, "score")
	sort.Strings(scores)
	if scores[0] != "10" || scores[1] != "30" {
		t.Errorf("scores = %v, want [10 30]", scores)
	}
}

func TestFindGte(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"score": "30"}))
	c.Insert(NewDocument("2", map[string]string{"score": "50"}))
	c.Insert(NewDocument("3", map[string]string{"score": "10"}))

	results := c.Find(Where("score", "gte", "30"))
	if len(results) != 2 {
		t.Fatalf("gte 30: got %d results, want 2", len(results))
	}
}

func TestFindMultipleConditions(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"city": "NYC", "dept": "eng"}))
	c.Insert(NewDocument("2", map[string]string{"city": "LA", "dept": "eng"}))
	c.Insert(NewDocument("3", map[string]string{"city": "NYC", "dept": "sales"}))
	c.Insert(NewDocument("4", map[string]string{"city": "LA", "dept": "sales"}))

	results := c.Find(Where("city", "eq", "NYC").And("dept", "eq", "eng"))
	if len(results) != 1 {
		t.Fatalf("AND query: got %d results, want 1", len(results))
	}
	if results[0].ID != "1" {
		t.Errorf("got doc %q, want %q", results[0].ID, "1")
	}
}

func TestUpdateReindexes(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"name": "Alice", "city": "NYC"}))
	c.Insert(NewDocument("2", map[string]string{"name": "Bob", "city": "NYC"}))

	if err := c.Update("1", map[string]string{"city": "LA"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	results := c.Find(Where("city", "eq", "NYC"))
	if len(results) != 1 {
		t.Fatalf("city=NYC after update: got %d results, want 1", len(results))
	}
	if results[0].Get("name") != "Bob" {
		t.Errorf("name = %q, want %q", results[0].Get("name"), "Bob")
	}
}

func TestUpdateAndFindOldValue(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"status": "active"}))

	if err := c.Update("1", map[string]string{"status": "archived"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	results := c.Find(Where("status", "eq", "active"))
	if len(results) != 0 {
		t.Fatalf("old value after update: got %d results, want 0", len(results))
	}
}

func TestUpdatePreservesOtherFields(t *testing.T) {
	c := NewCollection()
	c.Insert(NewDocument("1", map[string]string{"name": "Alice", "city": "NYC", "age": "30"}))

	if err := c.Update("1", map[string]string{"city": "LA"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	doc := c.Get("1")
	if doc == nil {
		t.Fatal("Get returned nil after update")
	}
	if doc.Get("name") != "Alice" {
		t.Errorf("name = %q, want %q", doc.Get("name"), "Alice")
	}
	if doc.Get("age") != "30" {
		t.Errorf("age = %q, want %q", doc.Get("age"), "30")
	}
	if doc.Get("city") != "LA" {
		t.Errorf("city = %q, want %q", doc.Get("city"), "LA")
	}
}

// helpers

func docNames(docs []*Document) []string {
	names := make([]string, len(docs))
	for i, d := range docs {
		names[i] = d.Get("name")
	}
	return names
}

func docField(docs []*Document, field string) []string {
	vals := make([]string, len(docs))
	for i, d := range docs {
		vals[i] = d.Get(field)
	}
	return vals
}
