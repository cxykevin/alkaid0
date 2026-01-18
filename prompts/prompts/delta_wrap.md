{{/* AI response 占位符 */}}
{{- if eq .Thinking ""}}
<think>
{{.Thinking}}
</think>
{{- end }}
{{.Delta}}
{{- if eq .ToolsCall ""}}
<tools>
{{.ToolsCall}}
</tools>
{{- end }}