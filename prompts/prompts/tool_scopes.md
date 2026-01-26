{{/* 工具作用域 */}}
{{if .Scopes}}
#### Scopes

{{if gt (len .Scopes) 0}}
<scopes>
    {{range .Scopes}}
    <scope id="{{.ID}}" enable="{{if .Enable}}true{{else}}false{{end}}">{{.Description}}</scope>
    {{end}}
</scopes>
{{end}}
{{end}}