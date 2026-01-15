package prompts

import (
	"testing"
	"text/template"
)

func TestPrompts(t *testing.T) {
	if Global == "" {
		t.Error("Global prompt is empty")
	}
	if Tools == "" {
		t.Error("Tools prompt is empty")
	}
	if DefaultAgent == "" {
		t.Error("DefaultAgent prompt is empty")
	}
}

func TestRender(t *testing.T) {
	tmpl := template.Must(template.New("test").Parse("hello {{.Name}}"))
	data := struct{ Name string }{Name: "world"}
	got := Render(tmpl, data)
	if got != "hello world" {
		t.Errorf("Render() = %q; want %q", got, "hello world")
	}
}

func TestTemplateFuncs(t *testing.T) {
	if toInt("123") != 123 {
		t.Errorf("toInt('123') = %d; want 123", toInt("123"))
	}
	if toInt(123.45) != 123 {
		t.Errorf("toInt(123.45) = %d; want 123", toInt(123.45))
	}
	if sub(10, 3) != 7 {
		t.Errorf("sub(10, 3) = %d; want 7", sub(10, 3))
	}
	if toString(123) != "123" {
		t.Errorf("toString(123) = %q; want '123'", toString(123))
	}
	if le(5, 10) != true {
		t.Errorf("le(5, 10) = false; want true")
	}
	if gt(10, 5) != true {
		t.Errorf("gt(10, 5) = false; want true")
	}
}
