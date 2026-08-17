package structs

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEmbeddingRequestPreservesInputForms(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(*testing.T, EmbeddingRequest)
	}{
		{
			name: "string",
			raw:  `{"input":"hello","model":"embed","dimensions":3}`,
			check: func(t *testing.T, req EmbeddingRequest) {
				if len(req.Input) != 0 || string(req.InputRaw) != `"hello"` {
					t.Fatalf("input = %#v, raw = %s", req.Input, req.InputRaw)
				}
			},
		},
		{
			name: "strings",
			raw:  `{"input":["a","b"],"model":"embed"}`,
			check: func(t *testing.T, req EmbeddingRequest) {
				if !reflect.DeepEqual(req.Input, []string{"a", "b"}) {
					t.Fatalf("input = %#v", req.Input)
				}
			},
		},
		{
			name: "tokens",
			raw:  `{"input":[1,2,3],"model":"embed"}`,
			check: func(t *testing.T, req EmbeddingRequest) {
				if len(req.Input) != 0 || string(req.InputRaw) != `[1,2,3]` {
					t.Fatalf("input = %#v, raw = %s", req.Input, req.InputRaw)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req EmbeddingRequest
			if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			tc.check(t, req)
			encoded, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("decode marshaled request: %v", err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.raw), &want); err != nil {
				t.Fatalf("decode expected request: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip = %s, want %s", encoded, tc.raw)
			}
		})
	}
}

func TestEmbeddingRequestMarshalUsesInput(t *testing.T) {
	req := EmbeddingRequest{Input: []string{"hello"}, Model: "embed", Dimensions: new(8)}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got["input"], []any{"hello"}) {
		t.Fatalf("input = %#v", got["input"])
	}
	if got["dimensions"] != float64(8) {
		t.Fatalf("dimensions = %#v", got["dimensions"])
	}
}

//go:fix inline
func intPtr(v int) *int { return new(v) }
