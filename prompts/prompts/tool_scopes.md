{{if .Scopes}}

### Tool Scopes

`[X]` means the scope is enabled.
`[ ]` means the scope is disabled.

#### Scopes

{{if gt (len .Scopes) 0}}{{range .Scopes}}
- {{if .Enable}}[X]{{else}}[ ]{{end}} `{{.ID}}`: {{.Description}}
{{end}}{{end}}{{end}}