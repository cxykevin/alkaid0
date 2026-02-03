#### Available Agent **Instances**

<agents>
{{range .Agents}}
    <instance name="{{.Name}}" path="{{.Path}}" tag="{{.Tag}}"/>
{{end}}
</agents>

#### Available Agent **Tags**

<agent_tags>
{{range .Tags}}
    <tag tag_name="{{.Name}}">{{.Description}}</tag>
{{end}}
</agent_tags>