package parser_test

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
)

func newParser() *parser.Parser {
	return parser.NewParser(nil, nil)
}

func TestNewParser(t *testing.T) {
	if p := newParser(); p == nil {
		t.Fatal("parser creation failed")
	}
}

func TestAddTokenNormalText(t *testing.T) {
	p := newParser()
	response, thinking, _, err := p.AddToken("Hello World", "")
	if err != nil {
		t.Fatalf("AddToken failed: %v", err)
	}
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken failed: %v", err)
	}
	if response+response2 != "Hello World" {
		t.Fatalf("response = %q", response+response2)
	}
	if thinking+thinking2 != "" {
		t.Fatalf("thinking = %q", thinking+thinking2)
	}
}

func TestAddTokenThinkTag(t *testing.T) {
	p := newParser()
	response, thinking, _, err := p.AddToken("<think>这是思考内容</think>", "")
	if err != nil {
		t.Fatalf("AddToken failed: %v", err)
	}
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken failed: %v", err)
	}
	if response+response2 != "" {
		t.Fatalf("response = %q", response+response2)
	}
	if thinking+thinking2 != "这是思考内容" {
		t.Fatalf("thinking = %q", thinking+thinking2)
	}
}

func TestAddTokenMixedContent(t *testing.T) {
	p := newParser()
	response, thinking, _, err := p.AddToken("普通文本\n<think>思考内容</think>更多文本", "")
	if err != nil {
		t.Fatalf("AddToken failed: %v", err)
	}
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken failed: %v", err)
	}
	if got := response + response2; got != "普通文本\n更多文本" {
		t.Fatalf("response = %q", got)
	}
	if got := thinking + thinking2; got != "思考内容" {
		t.Fatalf("thinking = %q", got)
	}
}

func TestParserThinkNotFull(t *testing.T) {
	p := newParser()
	response, thinking, _, err := p.AddToken("aaaa\n<think>内容</inner></outer>", "")
	if err != nil {
		t.Fatalf("AddToken failed: %v", err)
	}
	if response != "aaaa\n" {
		t.Fatalf("response = %q", response)
	}
	if thinking != "内容</inner></outer>" {
		t.Fatalf("thinking = %q", thinking)
	}
}

func TestParserMultipleAddTokens(t *testing.T) {
	p := newParser()
	var response, thinking strings.Builder
	for _, token := range []string{"第一段文本开始", "\n<think>思考内容", "</think>继续文本"} {
		r, th, _, err := p.AddToken(token, "")
		if err != nil {
			t.Fatalf("AddToken failed: %v", err)
		}
		response.WriteString(r)
		thinking.WriteString(th)
	}
	r, th, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken failed: %v", err)
	}
	response.WriteString(r)
	thinking.WriteString(th)
	if response.String() != "第一段文本开始\n继续文本" {
		t.Fatalf("response = %q", response.String())
	}
	if thinking.String() != "思考内容" {
		t.Fatalf("thinking = %q", thinking.String())
	}
}

func TestParserTagsRequireLineStart(t *testing.T) {
	cases := []struct {
		name      string
		tokens    []string
		wantResp  string
		wantThink string
	}{
		{name: "mid-line think", tokens: []string{"普通文本<think>不应识别</think>"}, wantResp: "普通文本<think>不应识别</think>"},
		{name: "indented think", tokens: []string{"  <think>不应识别</think>"}, wantResp: "  <think>不应识别</think>"},
		{name: "line-start think", tokens: []string{"第一行\n<think>识别</think>"}, wantResp: "第一行\n", wantThink: "识别"},
		{name: "split line-start think", tokens: []string{"\n<thi", "nk>识别</th", "ink>"}, wantResp: "\n", wantThink: "识别"},
		{name: "split mid-line think", tokens: []string{"abc<thi", "nk>不识别</th", "ink>"}, wantResp: "abc<think>不识别</think>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newParser()
			var response, thinking strings.Builder
			for _, token := range tc.tokens {
				r, th, _, err := p.AddToken(token, "")
				if err != nil {
					t.Fatalf("AddToken failed: %v", err)
				}
				response.WriteString(r)
				thinking.WriteString(th)
			}
			r, th, _, err := p.DoneToken()
			if err != nil {
				t.Fatalf("DoneToken failed: %v", err)
			}
			response.WriteString(r)
			thinking.WriteString(th)
			if response.String() != tc.wantResp {
				t.Fatalf("response = %q, want %q", response.String(), tc.wantResp)
			}
			if thinking.String() != tc.wantThink {
				t.Fatalf("thinking = %q, want %q", thinking.String(), tc.wantThink)
			}
		})
	}
}

func BenchmarkParserAddToken(b *testing.B) {
	p := newParser()
	for range b.N {
		_, _, _, _ = p.AddToken("这是一个测试 token\n<think>思考内容</think>更多内容", "")
	}
}

func BenchmarkParserDoneToken(b *testing.B) {
	p := newParser()
	for range b.N {
		_, _, _, _ = p.DoneToken()
	}
}
