<!-- Alkaid User Prompt -->
<user_prompt>
{{.Prompt}}
</user_prompt>

{{- if .Refers}}
{{- if gt (len .Refers) 0}}
<refers>
{{range .Refers}}
    {{if eq (toInt .FileType) 0}}
    <refer type="file">
        <path>{{.FilePath}}</path>
        <position>(Ln {{.FileFromLine}}, Col {{.FileFromCol}}) - (Ln {{.FileToLine}}, Col {{.FileToCol}})</position>
        {{if le (sub .FileToLine .FileFromLine) 3}}
        <text>
            <![CDATA[
{{string .Origin}}
]]>
        </text>
        {{end}}
    </refer>
    {{else if eq (toInt .FileType) 1}}
    <refer type="text">
        <text>
            <![CDATA[
{{string .Origin}}
]]>
        </text>
    </refer>
    {{else}}
    <refer type="unknown"></refer>
    {{end}}
{{end}}
</refers>
{{- end}}
{{- end}}