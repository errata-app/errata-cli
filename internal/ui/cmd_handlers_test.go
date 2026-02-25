package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFmtPrice(t *testing.T) {
	tests := []struct {
		name   string
		input  float64
		expect string
	}{
		{"whole dollars", 15.0, "$15.00"},
		{"dollars with cents", 2.50, "$2.50"},
		{"one dollar", 1.0, "$1.00"},
		{"sub-dollar round", 0.80, "$0.80"},
		{"sub-dollar exact", 0.15, "$0.15"},
		{"sub-dollar 60c", 0.60, "$0.60"},
		{"sub-dollar 50c", 0.50, "$0.50"},
		{"sub-cent", 0.075, "$0.075"},
		{"sub-cent small", 0.0075, "$0.0075"},
		{"one cent", 0.01, "$0.01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, fmtPrice(tt.input))
		})
	}
}
