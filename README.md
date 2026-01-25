# Alkaid0

Alkaid0 是一个模块化的 AI Coding 工具 与 Agent 框架，专为构建具备多 Agent 能力、工具调用系统和流式响应处理功能的智能编码助手而设计。该框架基于 Go 语言构建。

设计理念：**低消耗** **用户友好** **可扩展** **强兼容性**

### 日志（test only）

路径 `~/.config/alkaid0/log.log`

> 日志经过脱敏处理（脱去 Provider URL / KEY），但会保留请求的 Model ID 和 Agent Name 以及完整输入输出。日志不会携带工作区信息但 AI 模型的输出可能会包含部分用户代码。

提供 toolkit 包以查看日志。用法见其 `--help`。

### 配置（test only）

路径 `~/.config/alkaid0/config.json`

```json
{
    "Version": 1,
    "Model": {
        "ProviderURL": "https://openrouter.com/api/v1（这里暂时没有用）",
        "ProviderKey": "sk-or-xxx（这里暂时没有用）",
        "DefaultModelID": 1,
        "Models": {
            "1": {
                "ModelName": "模型名",
                "ModelID": "模型ID",
                "ProviderURL": "https://模型供应商/v1",
                "ProviderKey": "sk-模型密钥",
                "EnableThinking": true
            }
        }
    },
    "Agent": {
        "Agents": {
            "frontend": {
                "AgentName": "前端工程",
                "AgentDescription": "前端工程Agent",
                "AgentPrompt": "你是一个前端工程师，请根据用户的需求，提供前端工程解决方案。",
                "AgentModel": 1
            }
        },
        "GlobalPrompt": "始终使用中文回答",
        "SummaryModel": 1
    },
    "ThemeID": 0
}
```
