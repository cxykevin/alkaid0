package prompts

import (
	"bytes"
	"text/template"
)

// Render 渲染模板
func Render(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
