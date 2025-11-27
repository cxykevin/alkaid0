package parser

// JSONParser json 流式解析器
type JSONParser struct {
	FullCallingObject any
}

// AddToken 流式传入 token
func (p *JSONParser) AddToken(token string) (any, error) {
}

// DoneToken 传入结束 token
func (p *JSONParser) DoneToken() error {
}

// NewJSONParser 创建解析器
func NewJSONParser() *JSONParser {
	return &JSONParser{}
}
