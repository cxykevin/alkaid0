package main

import (
	"testing"
)

func TestParseLogLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected *LogEntry
	}{
		{
			name: "valid log line",
			line: "2025/12/07 14:04:35 [INFO][log] log inited",
			expected: &LogEntry{
				Timestamp: "2025/12/07 14:04:35",
				Level:     "INFO",
				Category:  "log",
				Message:   "log inited",
				Line:      "2025/12/07 14:04:35 [INFO][log] log inited",
			},
		},
		{
			name:     "invalid log line",
			line:     "invalid line",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLogLine(tt.line)
			if result == nil && tt.expected == nil {
				return
			}
			if result == nil || tt.expected == nil {
				t.Errorf("parseLogLine() = %v, expected %v", result, tt.expected)
				return
			}
			if *result != *tt.expected {
				t.Errorf("parseLogLine() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}

func TestShouldDisplay(t *testing.T) {
	tests := []struct {
		name     string
		entry    *LogEntry
		minLevel string
		expected bool
	}{
		{
			name: "DEBUG with DEBUG min",
			entry: &LogEntry{
				Level: "DEBUG",
			},
			minLevel: "DEBUG",
			expected: true,
		},
		{
			name: "INFO with DEBUG min",
			entry: &LogEntry{
				Level: "INFO",
			},
			minLevel: "DEBUG",
			expected: true,
		},
		{
			name: "DEBUG with INFO min",
			entry: &LogEntry{
				Level: "DEBUG",
			},
			minLevel: "INFO",
			expected: false,
		},
		{
			name: "unknown level",
			entry: &LogEntry{
				Level: "UNKNOWN",
			},
			minLevel: "DEBUG",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldDisplay(tt.entry, tt.minLevel)
			if result != tt.expected {
				t.Errorf("shouldDisplay() = %v, expected %v", result, tt.expected)
			}
		})
	}
}