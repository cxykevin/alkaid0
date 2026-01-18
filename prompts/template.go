package prompts

import (
	"text/template"
)

var funcMap = template.FuncMap{
	"toInt":  toInt,
	"sub":    sub,
	"string": toString,
	"le":     le,
	"gt":     gt,
}

// Load 加载模板
func Load(name string, origin string) *template.Template {
	return template.Must(template.New(name).Funcs(funcMap).Parse(origin))
}

func init() {
	initTemplates()
}
