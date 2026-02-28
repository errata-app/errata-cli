// Package uid provides type-prefixed UUID v7 generation for all Errata IDs.
package uid

import "github.com/google/uuid"

// New returns a type-prefixed UUID v7 string.
// Prefix should be a short tag like "ses_", "run_", "rpt_".
func New(prefix string) string {
	return prefix + uuid.Must(uuid.NewV7()).String()
}
