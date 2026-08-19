package run

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDetectOpenAIImport(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_PYTHON") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON not enabled")
	}

	// 需要真实的 Python 环境来测试 AST 检测
	// 这里使用系统 Python 作为回退
	pythonCmd := "python3"
	if _, err := exec.LookPath(pythonCmd); err != nil {
		pythonCmd = "python"
		if _, err := exec.LookPath(pythonCmd); err != nil {
			t.Skip("no python interpreter found")
		}
	}

	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{
			name:     "import openai",
			code:     "import openai\nprint('hello')",
			expected: true,
		},
		{
			name:     "from openai import OpenAI",
			code:     "from openai import OpenAI\nclient = OpenAI()",
			expected: true,
		},
		{
			name:     "import openai.types",
			code:     "import openai.types\nprint('hello')",
			expected: true,
		},
		{
			name:     "no openai import",
			code:     "import sys\nprint('hello')",
			expected: false,
		},
		{
			name:     "openai in string",
			code:     "s = 'openai'\nprint(s)",
			expected: false,
		},
		{
			name:     "openai in comment",
			code:     "# import openai\nprint('hello')",
			expected: false,
		},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, pythonCmd, "-c", astChecker)
			cmd.Stdin = strings.NewReader(tt.code)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out

			if err := cmd.Run(); err != nil {
				t.Logf("ast check command error: %v (output: %s)", err, out.String())
			}

			output := strings.TrimSpace(out.String())
			result := strings.Contains(output, "YES")
			if result != tt.expected {
				t.Errorf("expected %v, got %v (output: %q) for code:\n%s", tt.expected, result, output, tt.code)
			}
		})
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"A=1", "B=2", "C=3"}
	overlay := []string{"B=20", "D=4"}

	result := mergeEnv(base, overlay)

	expected := map[string]string{
		"A": "1",
		"B": "20", // overwritten
		"C": "3",
		"D": "4",
	}

	if len(result) != len(expected) {
		t.Errorf("expected %d entries, got %d", len(expected), len(result))
	}

	resultMap := make(map[string]string)
	for _, pair := range result {
		k, v, _ := strings.Cut(pair, "=")
		resultMap[k] = v
	}

	for k, v := range expected {
		if resultMap[k] != v {
			t.Errorf("key %s: expected %s, got %s", k, v, resultMap[k])
		}
	}
}
