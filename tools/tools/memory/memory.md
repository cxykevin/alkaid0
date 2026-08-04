#### Virtual Object: `@memory`

The following is the persistent memory. Recall it to follow established decisions, constraints, and preferences in your work.

{{if .Project}}
###### Project Memory (`.alkaid0/MEMORY.md`)

<memory>
{{.Project}}
</memory>
{{end}}{{if .Global}}
###### Global Memory (`MEMORY.md` beside config)

<memory>
{{.Global}}
</memory>
{{end}}
