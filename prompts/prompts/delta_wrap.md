{{/* AI response 占位符 */}}
{{- if ne .Thinking ""}}
<think>
{{.Thinking}}
</think>
{{- end }}
{{.Delta}}
{{- if ne .ToolsCall ""}}
<tools>
{{.ToolsCall}}
</tools>
{{- end }}