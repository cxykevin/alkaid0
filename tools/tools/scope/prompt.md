### Tool: `scope`

#### Description:

Scope is a collection of tools that can be enabled or disabled. Enable a scope means the tool in the scope will be available.

Enable or disable the tools scopes.

> `""` is the default scope, it couldn't be disabled.

**DO NOT try to enable or disable a unknown scope!**
**DO NOT try to enable or disable a unknown scope!**
**DO NOT try to enable or disable a unknown scope!**

**When to disable a scope**: If you needn't use the tools in the scope in the future of the whole project, you can disable it.

#### Parameters:

- `name` (required): The **exact name** of the scope will be enabled or disabled.
- `disable` (optional): Disable the scopes. Default is false.


#### Quick Examples:
- Enable scope: `{"name":"swarm"}`
- Disable scope: `{"name":"lang.python","disable":true}`