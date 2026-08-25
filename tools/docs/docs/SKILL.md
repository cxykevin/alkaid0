# dynworkflow Usage

## Purpose

Use `dynworkflow` to define and run dynamic workflows composed of multiple nodes. Nodes can execute regular Python logic or call `Agent` and `MultiAgent`.

## Import

```python
import dynworkflow as d
```

## Create a Flow

```python
flow = d.Flow("workflow-id")
```

The positional argument is the workflow ID. Agent connectivity is resolved by the runtime configuration.

## Define Nodes

```python
@flow.node("Node Name")
def node_name(value: int) -> d.NodeResult:
    return d.Result(value)
```

Calling a node function does not execute it immediately. It returns an `Execute` task. You may provide only part of the declared arguments; the node keeps those values until later calls fill the remaining arguments:

```python
@flow.node("Two Values")
def two_values(val1: int, val2: int) -> d.NodeResult:
    return d.Result(val1 + val2)

flow.run(two_values(val1=1), two_values(val2=2))       # both tasks fill the same node before it runs
```

A node is scheduled only after every declared argument has been filled. If `val1` and `val2` are declared, supplying only `val1` does not run the node or any downstream nodes; a later call must supply `val2` first. Default-valued parameters are considered filled by their defaults.

Supported node return values:

- `None`: finish the node.
- `d.Result(value)`: store a terminal result.
- One downstream task: continue with one node.
- A `list` or `tuple` of downstream tasks: continue with multiple nodes.

## Call Agents

Define a node as a generator when it needs an agent:

```python
@flow.node("Summarize")
def summarize() -> d.TaskGenerator:
    result = yield d.Agent(
        "Summarize the documents",
        model="",
        path="docs/test",
    )
    return d.Result(result)
```

The value received from `yield` is the agent result. Use `MultiAgent` for multiple concurrent agent requests:

```python
@flow.node("Calculate")
def calculate() -> d.TaskGenerator:
    results = yield d.MultiAgent(
        d.Agent("Calculate 1 + 1"),
        d.Agent("Calculate 2 + 2"),
    )
    return d.Result(results)
```

## Pass Values Between Nodes

```python
@flow.node("Start")
def start() -> d.NodeResult:
    return next_step(val1=1)

@flow.node("Next")
def next_step(val1: int, val2: int) -> d.NodeResult:
    return d.Result(val1 + val2)

# The downstream node remains pending until both val1 and val2 are filled.
flow.run(start())
```

A node can return multiple downstream tasks:

```python
return first(value=1), second(value=2)
```

## Run a Workflow

Independent nodes run concurrently. When a node fails, it is immediately marked `error` and reported when reporting is enabled, but sibling nodes continue running. After all nodes finish, collected failures are raised together as an `ExceptionGroup`.

```python
flow.run(start())
```

## Caching

Persistent caching takes effect only when the relevant workflow, node, function source, session scope, and arguments can be represented in the cache key. A node whose source or arguments cannot be serialized is not cached. The cache is also bypassed when the cache backend is unavailable.

## Clean history value

`clean_cache()` does not clear persistent workflow or agent cache records. It clears the selected node's in-memory output-argument state so that its parameters can be filled again:

```python
node.clean_cache()               # clear all cached output arguments for this node
node.clean_cache("argument_name")  # clear one output argument
```
