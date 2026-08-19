package run

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/server/apikey"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/pythonenv"
)

// detectOpenAIImport 检测 Python 代码是否导入了 openai 模块。
// 使用 venv 内 Python 执行 AST 预检程序，支持：
//
//	import openai
//	from openai import *
//	import openai.xxx
func detectOpenAIImport(ctx context.Context, code string) (bool, error) {
	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		return false, fmt.Errorf("python venv not initialized")
	}

	astChecker := `import sys, ast
code = sys.stdin.read()
try:
    tree = ast.parse(code)
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name == 'openai' or alias.name.startswith('openai.'):
                    print('YES')
                    sys.exit(0)
        elif isinstance(node, ast.ImportFrom):
            if node.module == 'openai' or (node.module and node.module.startswith('openai.')):
                print('YES')
                sys.exit(0)
    print('NO')
except:
    print('NO')
`

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, venvPython, "-c", astChecker)
	cmd.Stdin = strings.NewReader(code)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("ast check failed: %w (output: %s)", err, out.String())
	}

	return strings.Contains(out.String(), "YES"), nil
}

// resolveCurrentModel 解析当前 session 的模型配置，返回字符串 ModelID。
// 优先使用 active subagent 的模型，否则使用 session.LastModelID。
func resolveCurrentModel(session *storageStructs.Chats) (string, error) {
	modelID := session.LastModelID
	if session.CurrentAgentID != "" {
		agentModelID := uint32(session.CurrentAgentConfig.AgentModel)
		if agentModelID != 0 {
			modelID = agentModelID
		}
	}

	modelCfg, ok := config.GlobalConfig.Model.Models[int32(modelID)]
	if !ok {
		return "", fmt.Errorf("model %d not found in config", modelID)
	}
	if modelCfg.ModelID == "" {
		return "", fmt.Errorf("model %d has empty ModelID", modelID)
	}

	return modelCfg.ModelID, nil
}

// buildProxyEnv 构造 OpenAI proxy 环境变量。
// 返回：OPENAI_API_KEY、OPENAI_BASE_URL、OPENAI_MODEL_ID。
func buildProxyEnv(session *storageStructs.Chats) (key, baseURL, modelID string, err error) {
	modelID, err = resolveCurrentModel(session)
	if err != nil {
		return "", "", "", err
	}

	key, err = apikey.New(30) // 30 分钟有效期
	if err != nil {
		return "", "", "", fmt.Errorf("create temporary API key: %w", err)
	}

	port := config.GlobalConfig.Server.Port
	host := strings.TrimSpace(config.GlobalConfig.Server.Host)
	// 通配地址不能作为客户端目标，改用 loopback；其他地址与服务监听配置保持一致。
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	baseURL = fmt.Sprintf("http://%s/openai/v1", net.JoinHostPort(host, fmt.Sprint(port)))

	return key, baseURL, modelID, nil
}

// mergeEnv 合并环境变量，新值覆盖同名旧值。
func mergeEnv(base, overlay []string) []string {
	m := make(map[string]string)
	for _, pair := range base {
		k, v, _ := strings.Cut(pair, "=")
		m[k] = v
	}
	for _, pair := range overlay {
		k, v, _ := strings.Cut(pair, "=")
		m[k] = v
	}
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}
