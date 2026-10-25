package structs

import "testing"

func TestParseHistoryPath(t *testing.T) {
	tests := []struct {
		path   string
		id     uint32
		wantOK bool
	}{
		{"@history/538", 538, true},
		{"@history/1", 1, true},
		{"@history/0", 0, false},
		{"@history/", 0, false},
		{"@history/abc", 0, false},
		{"@history/538/extra", 0, false},
		{"@history\\538", 0, false},
		{"@tree", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		id, ok := ParseHistoryPath(tt.path)
		if ok != tt.wantOK || id != tt.id {
			t.Errorf("ParseHistoryPath(%q) = (%d, %v), want (%d, %v)", tt.path, id, ok, tt.id, tt.wantOK)
		}
		if IsHistoryPath(tt.path) != tt.wantOK {
			t.Errorf("IsHistoryPath(%q) = %v, want %v", tt.path, IsHistoryPath(tt.path), tt.wantOK)
		}
	}
	if got := HistoryPath(538); got != "@history/538" {
		t.Errorf("HistoryPath(538) = %q", got)
	}
}
