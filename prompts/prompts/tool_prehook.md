{{if .Unused}}{{if gt (len .Unused) 0}}{{range .Unused}}
{{.}}
{{end}}{{end}}{{end}}
{{if .Active}}{{if gt (len .Active) 0}}{{range .Active}}
{{.}}
{{end}}{{end}}{{end}}