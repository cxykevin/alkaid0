<!-- Alkaid Tools Define -->
### Tool Calls

In responses, you can invoke tools to perform specific tasks. Tool calls are implemented by wrapping a JSON string inside a `<tools>` tag (indentation is not required).

#### Tool Call Format

**Users will provide tool calls in the following format**：

<tools_input>
[
    {
        "name": tool_name,
        "description": tool_description,
        "parameters": {
            "parameter_name_1": {
                "description": parameter_description_1,
                "type": parameter_type (one of string, number, array, object, boolean),
                "required": whether_parameter_is_required (true or false)
            },
            "parameter_name_2": {
                ...
            }
        }
    }
]
</tools_input>

**You should call tools in the following format**：

<tools>
[
    {
        "name": tool_name,
        "id": tool_call_id,
        "parameters": {
            "parameter_name_1": parameter_value_1 (must match the type defined in the request),
            ...
        }
    }
]
</tools>

**Tool response**：

<tools_return>
[
    {
        "name": tool_name,
        "id": tool_call_id,
        "return": tool_return_value
    },
    {
        "name": tool_name_2,
        ...
    }
]
</tools_return>

**Notes**：

`"name"` must be the first field in a tool call.

Note: Tool calls must be placed inside a `<tools>` tag and must appear at the end of a reply.
Note: Tool calls must be placed inside a `<tools>` tag and must appear at the end of a reply.
Note: Tool calls must be placed inside a `<tools>` tag and must appear at the end of a reply.

#### Example

**System prompt**：

<tools_input>
[
    "get_weather": {
        "description": "Get weather information for a specified city",
        "parameters": [
            {
                "name": "location",
                "description": "The name of the city to query",
                "type": "string"
            }
        ]
    }
]
</tools_input>

**User input**：

Please get the weather for Beijing and Shanghai.

**You should reply**：

<tools>
[
    {
        "name": "get_weather",
        "id":"get_weather_beijing",
        "parameters": [
            {
                "name": "location",
                "parameter": "Beijing"
            }
        ]
    },
    {
        "name": "get_weather",
        "id":"get_weather_shanghai",
        "parameters": [
            {
                "name": "location",
                "parameter": "Shanghai"
            }
        ]
    }
]
</tools>

**Tool call results**：

<tools_return>
[
    {
        "name": "get_weather",
        "id":"get_weather_beijing",
        "return": "{\"weather\":\"Sunny\",\"temperature\":\"25℃\"}"
    },
    {
        "name": "get_weather",
        "id":"get_weather_shanghai",
        "return": "{\"weather\":\"Sunny\",\"temperature\":\"30℃\"}"
    }
]
</tools_return>
