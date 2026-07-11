package main

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestColorize(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		color   string
		noColor bool
		want    string
	}{
		{name: "with color", text: "test", color: Red, noColor: false, want: Red + "test" + Reset},
		{name: "no color", text: "test", color: Red, noColor: true, want: "test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := colorize(tt.text, tt.color, tt.noColor)
			if got != tt.want {
				t.Errorf("colorize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHighlightLevel(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		noColor bool
	}{
		{name: "DEBUG", level: "DEBUG", noColor: false},
		{name: "INFO", level: "INFO", noColor: false},
		{name: "WARN", level: "WARN", noColor: false},
		{name: "ERROR", level: "ERROR", noColor: false},
		{name: "UNKNOWN", level: "UNKNOWN", noColor: false},
		{name: "DEBUG no color", level: "DEBUG", noColor: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highlightLevel(tt.level, tt.noColor)
			if tt.noColor && got != "["+tt.level+"]" {
				t.Errorf("highlightLevel() = %q, want %q", got, "["+tt.level+"]")
			}
			if !tt.noColor && got == "" {
				t.Error("highlightLevel() should not be empty")
			}
		})
	}
}

func TestHighlightCategory(t *testing.T) {
	got := highlightCategory("test", false)
	if got == "" {
		t.Error("highlightCategory() should not be empty")
	}
	gotNoColor := highlightCategory("test", true)
	if gotNoColor != "[test]" {
		t.Errorf("highlightCategory noColor = %q, want %q", gotNoColor, "[test]")
	}
}

func TestHighlightTimestamp(t *testing.T) {
	got := highlightTimestamp("2025/01/01 12:00:00", false)
	if got == "" {
		t.Error("highlightTimestamp() should not be empty")
	}
	gotNoColor := highlightTimestamp("2025/01/01 12:00:00", true)
	if gotNoColor != "2025/01/01 12:00:00" {
		t.Errorf("highlightTimestamp noColor = %q, want %q", gotNoColor, "2025/01/01 12:00:00")
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()

	emptyFile := filepath.Join(dir, "empty.log")
	os.WriteFile(emptyFile, []byte{}, 0644)
	if got := countLines(emptyFile); got != 0 {
		t.Errorf("countLines(empty) = %d, want 0", got)
	}

	threeLines := filepath.Join(dir, "three.log")
	os.WriteFile(threeLines, []byte("line1\nline2\nline3\n"), 0644)
	if got := countLines(threeLines); got != 3 {
		t.Errorf("countLines(3 lines) = %d, want 3", got)
	}

	if got := countLines("/nonexistent/path.log"); got != 0 {
		t.Errorf("countLines(nonexistent) = %d, want 0", got)
	}
}

func TestDisplayLogEntry(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	entry := &LogEntry{
		Timestamp: "2025/01/01 12:00:00",
		Level:     "INFO",
		Category:  "test",
		Message:   "hello world",
		Line:      "2025/01/01 12:00:00 [INFO][test] hello world",
	}
	displayLogEntry(entry, Config{NoColor: true})

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	if buf.String() == "" {
		t.Error("displayLogEntry should produce output")
	}
}

func TestNewFileWatcher(t *testing.T) {
	fw := newFileWatcher("/test/path.log", Config{MinLevel: "DEBUG"})
	if fw == nil {
		t.Fatal("newFileWatcher should return non-nil")
	}
	if fw.filePath != "/test/path.log" {
		t.Errorf("filePath = %q, want %q", fw.filePath, "/test/path.log")
	}
}
