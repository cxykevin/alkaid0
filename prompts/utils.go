package prompts

import (
	"bytes"
	"text/template"
)

// Render 渲染模板（error 直接 panic）
func Render(tmpl *template.Template, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}
