package parser_test

import (
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
)

// 测试工具定义
var testTools = []parser.ToolsDefine{
	{
		Name:        "calculator",
		Description: "简单的计算器工具",
		Parameters: map[string]parser.ToolParameters{
			"expression": {
				Type:        parser.ToolTypeString,
				Required:    true,
				Description: "要计算的表达式",
			},
			"precision": {
				Type:        parser.ToolTypeNumber,
				Required:    false,
				Description: "计算精度",
			},
		},
	},
	{
		Name:        "weather",
		Description: "获取天气信息",
		Parameters: map[string]parser.ToolParameters{
			"city": {
				Type:        parser.ToolTypeString,
				Required:    true,
				Description: "城市名称",
			},
		},
	},
}

// TestNewParser 测试解析器创建
func TestNewParser(t *testing.T) {
	p := parser.NewParser(testTools)
	if p == nil {
		t.Fatal("解析器创建失败")
	}
	if len(p.Tools) != len(testTools) {
		t.Errorf("期望 %d 个工具，实际 %d 个", len(testTools), len(p.Tools))
	}
}

// TestAddTokenNormalText 测试普通文本解析
func TestAddTokenNormalText(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试普通文本
	response, thinking, _, err := p.AddToken("Hello World")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	if response != "Hello World" {
		t.Errorf("期望响应 'Hello World'，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}
}

// TestAddTokenThinkTag 测试 think 标签解析
func TestAddTokenThinkTag(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试 think 标签
	token := "<think>这是思考内容</think>"
	response, thinking, _, err := p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	if response != "" {
		t.Errorf("期望响应为空，实际 '%s'", response)
	}
	if thinking != "这是思考内容" {
		t.Errorf("期望思考内容 '这是思考内容'，实际 '%s'", thinking)
	}
}

// // TestAddTokenToolsTag 测试 tools 标签解析
// func TestAddTokenToolsTag(t *testing.T) {
// 	p := parser.NewParser(testTools)

// 	// 测试 tools 标签
// 	token := "<tools>\n{\n  \"name\": \"calculator\",\n  \"parameters\": {\n    \"expression\": \"2+2\"\n  }\n}\n</tools>"
// 	response, thinking, tools, err := p.AddToken(token)
// 	if err != nil {
// 		t.Fatalf("解析失败: %v", err)
// 	}

// 	if response != "" {
// 		t.Errorf("期望响应为空，实际 '%s'", response)
// 	}
// 	if thinking != "" {
// 		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
// 	}
// 	if tools == nil {
// 		t.Errorf("期望工具不为 nil，实际为 nil")
// 	}
// }

// TestAddTokenMixedContent 测试混合内容解析
func TestAddTokenMixedContent(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试混合内容
	token := "普通文本<think>思考内容</think>更多文本结尾文本"
	response, thinking, _, err := p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	expectedResponse := "普通文本更多文本结尾文本"
	expectedThinking := "思考内容"

	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}
	// if tools == nil {
	// 	t.Errorf("期望工具不为 nil，实际为 nil")
	// }
}

// TestDoneToken 测试结束 token 处理
func TestDoneToken(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试不同状态下的 DoneToken
	testCases := []struct {
		name             string
		mode             int16
		keyMode          int16
		tokenCache       string
		expectedResponse string
		expectedThinking string
	}{
		{
			name:             "标签外状态",
			mode:             0,
			keyMode:          1,
			tokenCache:       "",
			expectedResponse: "",
			expectedThinking: "",
		},
		{
			name:             "入标签状态",
			mode:             1,
			keyMode:          1,
			tokenCache:       "unclosed",
			expectedResponse: "<unclosed",
			expectedThinking: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p.Mode = tc.mode
			p.KeyMode = tc.keyMode
			p.TokenCache = tc.tokenCache

			response, thinking, tools, err := p.DoneToken()
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}

			if response != tc.expectedResponse {
				t.Errorf("期望响应 '%s'，实际 '%s'", tc.expectedResponse, response)
			}
			if thinking != tc.expectedThinking {
				t.Errorf("期望思考内容 '%s'，实际 '%s'", tc.expectedThinking, thinking)
			}
			if tools != nil {
				t.Errorf("期望工具为 nil，实际不为 nil")
			}
		})
	}
}

// TestParserEdgeCases 测试边界情况
func TestParserEdgeCases(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试超长标签
	longTag := "<" + string(make([]byte, 10)) + ">" // 超过 maxTagLen
	response, _, _, err := p.AddToken(longTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, _, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2

	// 测试嵌套标签（应该按普通文本处理）
	nestedTag := "<outer><inner>内容</inner></outer>"
	response, _, _, err = p.AddToken(nestedTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "<outer><inner>内容</inner></outer>"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
}

// BenchmarkParserAddToken 性能测试
func BenchmarkParserAddToken(b *testing.B) {
	p := parser.NewParser(testTools)
	testToken := "这是一个测试token<think>思考内容</think>更多内容"

	for b.Loop() {
		p.AddToken(testToken)
	}
}

// BenchmarkParserDoneToken 性能测试
func BenchmarkParserDoneToken(b *testing.B) {
	p := parser.NewParser(testTools)

	for b.Loop() {
		p.DoneToken()
	}
}

// TestParserThinkNotFull 测试思考不完整边界情况
func TestParserThinkNotFull(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试嵌套标签（应该按普通文本处理）
	nestedTag := "aaaa<think>内容</inner></outer>"
	response, thinking, _, err := p.AddToken(nestedTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "aaaa"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking := "内容</inner></outer>"
	if thinking != expectedThinking {
		t.Errorf("期望思考 '%s'，实际 '%s'", expectedThinking, thinking)
	}

	p = parser.NewParser(testTools)
	// 测试嵌套标签（应该按普通文本处理）
	nestedTag = "aaaa<think>内容</inner></outer></aaaaaaaa"
	response, thinking, _, err = p.AddToken(nestedTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	expectedResponse = "aaaa"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking = "内容</inner></outer></aaaaaaaa"
	if thinking != expectedThinking {
		t.Errorf("期望思考 '%s'，实际 '%s'", expectedThinking, thinking)
	}

	p = parser.NewParser(testTools)
	// 测试嵌套标签（应该按普通文本处理）
	nestedTag = "aaaa<think>内容</inner></outer></think"
	response, thinking, _, err = p.AddToken(nestedTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response2, thinking2, _, err = p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	expectedResponse = "aaaa"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking = "内容</inner></outer></think"
	if thinking != expectedThinking {
		t.Errorf("期望思考 '%s'，实际 '%s'", expectedThinking, thinking)
	}
}

// // TestParserToolsTag 测试工具标签解析
// func TestParserToolsTag(t *testing.T) {
// 	p := parser.NewParser(testTools)

// 	// 测试 tools 标签
// 	toolsContent := `{
// 		"name": "calculator",
// 		"parameters": {
// 			"expression": "2+2"
// 		}
// 	}`
// 	token := "<tools>\n" + toolsContent + "\n</tools>"
// 	response, thinking, tools, err := p.AddToken(token)
// 	if err != nil {
// 		t.Fatalf("解析失败: %v", err)
// 	}

// 	if response != "" {
// 		t.Errorf("期望响应为空，实际 '%s'", response)
// 	}
// 	if thinking != "" {
// 		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
// 	}
// 	if tools == nil {
// 		t.Errorf("期望工具不为 nil，实际为 nil")
// 	}

// 	// 测试 tools 标签内包含普通文本
// 	tokenWithText := "<tools>普通文本内容</tools>"
// 	response, thinking, tools, err = p.AddToken(tokenWithText)
// 	if err != nil {
// 		t.Fatalf("解析失败: %v", err)
// 	}

// 	if response != "" {
// 		t.Errorf("期望响应为空，实际 '%s'", response)
// 	}
// 	if thinking != "" {
// 		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
// 	}
// }

// TestParserUnmatchedTags 测试不匹配标签
func TestParserUnmatchedTags(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试不匹配标签：只有开始标签
	token := "<think>内容没有结束标签"
	response, thinking, _, err := p.AddToken(token)

	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	if response != "" {
		t.Errorf("期望响应为空，实际 '%s'", response)
	}
	if thinking != "内容没有结束标签" {
		t.Errorf("期望思考内容 '内容没有结束标签'，实际 '%s'", thinking)
	}

	// 测试不匹配标签：结束标签没有开始
	p = parser.NewParser(testTools)
	token = "内容没有开始标签</think>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "内容没有开始标签</think>"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}

	// 测试错位标签
	p = parser.NewParser(testTools)
	token = "前缀<think>思考内容</tools>后缀"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err = p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	expectedResponse = "前缀"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking := "思考内容</tools>后缀"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}
}

// TestParserEmptyTags 测试空标签
func TestParserEmptyTags(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试空 think 标签
	token := "<think></think>"
	response, thinking, _, err := p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != "" {
		t.Errorf("期望响应为空，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}

	// 测试空 tools 标签
	token = "<tools></tools>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != "" {
		t.Errorf("期望响应为空，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}

	// 测试空字符串标签
	token = "<>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != "<>" {
		t.Errorf("期望响应 '<>'，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}
}

// TestParserSpecialCharacters 测试特殊字符处理
func TestParserSpecialCharacters(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试换行符
	token := "第一行\n第二行<think>思考包含\n换行符</think>"
	response, thinking, _, err := p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "第一行\n第二行"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking := "思考包含\n换行符"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}

	// 测试制表符
	p = parser.NewParser(testTools)
	token = "文本\t制表符<think>思考\t制表符</think>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse = "文本\t制表符"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking = "思考\t制表符"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}

	// 测试转义字符
	p = parser.NewParser(testTools)
	token = "文本\n\t\\<开始标签"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse = "文本\n\t\\<开始标签"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}
}

// TestParserChineseCharacters 测试中文字符处理
func TestParserChineseCharacters(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试包含中文的普通文本
	token := "这是一个中文测试文本<think>这是中文思考内容</think>继续中文文本"
	response, thinking, _, err := p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "这是一个中文测试文本继续中文文本"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking := "这是中文思考内容"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}

	// 测试纯中文标签（应该作为普通文本处理）
	p = parser.NewParser(testTools)
	token = "<中文标签>内容</中文标签>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != "<中文标签>内容</中文标签>" {
		t.Errorf("期望响应 '<中文标签>内容</中文标签>'，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}
}

// TestParserMultipleAddTokens 测试多次调用 AddToken
func TestParserMultipleAddTokens(t *testing.T) {
	p := parser.NewParser(testTools)

	// 分批添加内容
	responses := []string{}
	thinkings := []string{}

	// 第一批
	token1 := "第一段文本开始"
	response, thinking, _, err := p.AddToken(token1)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	responses = append(responses, response)
	thinkings = append(thinkings, thinking)

	// 第二批
	token2 := " 开始思考部分<think>思考内容"
	response, thinking, _, err = p.AddToken(token2)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	responses = append(responses, response)
	thinkings = append(thinkings, thinking)

	// 第三批
	token3 := " 结束思考部分</think>继续文本"
	response, thinking, _, err = p.AddToken(token3)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	responses = append(responses, response)
	thinkings = append(thinkings, thinking)
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	responses = append(responses, response2)
	thinkings = append(thinkings, thinking2)

	// 验证最终结果
	expectedTotalResponse := "第一段文本开始 开始思考部分继续文本"
	actualTotalResponse := ""
	for _, r := range responses {
		actualTotalResponse += r
	}

	if actualTotalResponse != expectedTotalResponse {
		t.Errorf("期望总响应 '%s'，实际 '%s'", expectedTotalResponse, actualTotalResponse)
	}

	expectedTotalThinking := "思考内容 结束思考部分"
	actualTotalThinking := ""
	for _, t := range thinkings {
		actualTotalThinking += t
	}

	if actualTotalThinking != expectedTotalThinking {
		t.Errorf("期望总思考内容 '%s'，实际 '%s'", expectedTotalThinking, actualTotalThinking)
	}

	// 测试 DoneToken
	response, thinking, _, err = p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if response != "" {
		t.Errorf("期望最终响应为空，实际 '%s'", response)
	}
	if thinking != "" {
		t.Errorf("期望最终思考内容为空，实际 '%s'", thinking)
	}
}

// TestParserExtremeLength 测试极端长度文本
func TestParserExtremeLength(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试超长普通文本
	longText := ""
	for range 10000 {
		longText += "a"
	}
	response, thinking, _, err := p.AddToken(longText)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != longText {
		t.Errorf("期望响应长度 %d，实际长度 %d", len(longText), len(response))
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}

	// 测试超长思考内容
	p = parser.NewParser(testTools)
	longThinkText := ""
	for range 10000 {
		longThinkText += "思"
	}
	token := "<think>" + longThinkText + "</think>"
	response, thinking, _, err = p.AddToken(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if response != "" {
		t.Errorf("期望响应为空，实际 '%s'", response)
	}
	if thinking != longThinkText {
		t.Errorf("期望思考内容长度 %d，实际长度 %d", len(longThinkText), len(thinking))
	}
}

// TestParserMaxTagLength 测试最大标签长度边界
func TestParserMaxTagLength(t *testing.T) {
	p := parser.NewParser(testTools)

	// 测试正好 maxTagLen 长度的标签
	fiveCharTag := "<think" // 正好 5 个字符 (<think)
	response, thinking, _, err := p.AddToken(fiveCharTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err := p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	if response != "<think" {
		t.Errorf("期望响应为'<think'，实际 '%s'", response)
	}

	p = parser.NewParser(testTools)
	// 测试超过 maxTagLen 长度的标签
	sixCharTag := "<thinks" // 超过 5 个字符
	response = ""
	response, thinking, _, err = p.AddToken(sixCharTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 结束
	response2, thinking2, _, err = p.DoneToken()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	response += response2
	thinking += thinking2

	expectedResponse := "<thinks"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	if thinking != "" {
		t.Errorf("期望思考内容为空，实际 '%s'", thinking)
	}
}

// TestParserComplexScenarios 测试复杂场景
func TestParserComplexScenarios(t *testing.T) {
	// 测试多个标签的复杂嵌套
	complexContent := "文本1<think>思考1</think>文本2\n换行内容<think>思考2\n多行内容</think><tools>工具内容</tools>\n后续文本"

	p := parser.NewParser(testTools)
	response, thinking, tools, err := p.AddToken(complexContent)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse := "文本1文本2\n换行内容\n后续文本"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking := "思考1思考2\n多行内容"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}
	if tools == nil {
		t.Errorf("期望工具不为 nil，实际为 nil")
	}

	// 测试标签在行末的情况
	p = parser.NewParser(testTools)
	lineEndTag := "行末标签<think>行末思考</think>"
	response, thinking, _, err = p.AddToken(lineEndTag)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	expectedResponse = "行末标签"
	if response != expectedResponse {
		t.Errorf("期望响应 '%s'，实际 '%s'", expectedResponse, response)
	}
	expectedThinking = "行末思考"
	if thinking != expectedThinking {
		t.Errorf("期望思考内容 '%s'，实际 '%s'", expectedThinking, thinking)
	}
}
