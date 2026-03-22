package run

import (
	"testing"
)

func TestAsInt32(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int32
		ok       bool
	}{
		{"int", 60, 60, true},
		{"float64", 60.0, 60, true},
		{"string int", "60", 60, true},
		{"string float", "60.0", 60, true},
		{"invalid string", "abc", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asInt32(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asInt32() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}
