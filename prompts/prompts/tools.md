### 工具调用

在回复中，你可以调用工具来执行特定的任务。工具调用通过在 `<tools>` 标签内包裹的 json 字符串实现（缩进不是强制要求）。

#### 工具调用格式

**用户会以如下格式输入工具调用**：

<tools_input>
[
    {
        "name": 工具名称,
        "description": 工具描述,
        "parameters": {
            "参数名1": {
                "description": 参数描述1,
                "type": 参数类型（取值 string, number, array, object, boolen）,
                "required": 参数是否必须（取值 true 或 false）
            },
            "参数名2": {
                ...
            }
        }
    }
]
</tools_input>

**你需要以如下方式调用工具**：

<tools>
[
    {
        "name": 工具名称,
        "id": 工具调用ID，用于区分同一请求不同的工具调用,
        "parameters": {
            "参数名1": 参数值1（必须和请求的数据类型相同）,
            ...
        }
    }
]
</tools>

**工具返回**：

<tools_return>
[
    {
        "name": 工具名称,
        "id": 工具调用ID，和请求时的ID一致,
        "return": 工具返回值，为一个字符串
    },
    {
        "name":工具名称2,
        ...
    }
]
</tools_return>

**注意事项**：

谨记：工具调用必须在 `<tools>` 标签内，并且位于一段回复的结尾。
谨记：工具调用必须在 `<tools>` 标签内，并且位于一段回复的结尾。
谨记：工具调用必须在 `<tools>` 标签内，并且位于一段回复的结尾。

#### 示例

**系统提示词**：

<tools_input>
[
    "get_weather": {
        "description": "获取指定城市的天气信息",
        "parameters": [
            {
                "name": "location",
                "description": "需要查询的城市名称",
                "type": "string"
            }
        ]
    }
]
</tools_input>

**用户输入**：

帮我查询北京和上海的天气。

**你需要回复**：

<tools>
[
    {
        "name": "get_weather",
        "id":"get_weather_beijing",
        "parameters": [
            {
                "name": "location",
                "parameter": "北京"
            }
        ]
    },
    {
        "name": "get_weather",
        "id":"get_weather_shanghai",
        "parameters": [
            {
                "name": "location",
                "parameter": "上海"
            }
        ]
    }
]
</tools>

**工具调用结果**：

<tools_return>
[
    {
        "name": "get_weather",
        "id":"get_weather_beijing",
        "return": "{\"weather\":""晴\",\"temperature\":\"25℃\"}"
    },
    {
        "name": "get_weather",
        "id":"get_weather_shanghai",
        "return": "{\"weather\":""晴\",\"temperature\":\"30℃\"}"
    },
]
</tools_return>

#### 可用工具

<tools_input>
{{.toolsJson}}
</tools_input>
