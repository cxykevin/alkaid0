package index

import (
	"testing"
)

func TestAddIndex(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	// 添加一个索引函数
	AddIndex(func() string {
		return "test_tool_1"
	})
	
	if len(PkgIndexs) != 1 {
		t.Errorf("Expected 1 index, got %d", len(PkgIndexs))
	}
	
	// 添加第二个索引函数
	AddIndex(func() string {
		return "test_tool_2"
	})
	
	if len(PkgIndexs) != 2 {
		t.Errorf("Expected 2 indexes, got %d", len(PkgIndexs))
	}
}

func TestLoad(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	// 用于跟踪调用
	called := []string{}
	
	// 添加多个索引函数
	AddIndex(func() string {
		called = append(called, "tool1")
		return "tool1"
	})
	
	AddIndex(func() string {
		called = append(called, "tool2")
		return "tool2"
	})
	
	AddIndex(func() string {
		called = append(called, "tool3")
		return "tool3"
	})
	
	// 执行加载
	Load()
	
	// 验证所有函数都被调用
	if len(called) != 3 {
		t.Errorf("Expected 3 calls, got %d", len(called))
	}
	
	// 验证调用顺序
	expectedOrder := []string{"tool1", "tool2", "tool3"}
	for i, name := range expectedOrder {
		if called[i] != name {
			t.Errorf("Expected %s at position %d, got %s", name, i, called[i])
		}
	}
}

func TestLoadEmpty(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	// 加载空列表不应该panic
	Load()
}

func TestMultipleAddAndLoad(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	counter := 0
	
	// 添加多个索引
	for i := 0; i < 5; i++ {
		AddIndex(func() string {
			counter++
			return "tool"
		})
	}
	
	if len(PkgIndexs) != 5 {
		t.Errorf("Expected 5 indexes, got %d", len(PkgIndexs))
	}
	
	// 加载所有索引
	Load()
	
	if counter != 5 {
		t.Errorf("Expected counter to be 5, got %d", counter)
	}
}

func TestIndexReturnValues(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	results := []string{}
	
	// 添加返回不同值的索引
	AddIndex(func() string {
		return "edit_tool"
	})
	
	AddIndex(func() string {
		return "tree_tool"
	})
	
	AddIndex(func() string {
		return "trace_tool"
	})
	
	// 手动调用每个索引函数并收集结果
	for _, index := range PkgIndexs {
		results = append(results, index())
	}
	
	expected := []string{"edit_tool", "tree_tool", "trace_tool"}
	if len(results) != len(expected) {
		t.Fatalf("Expected %d results, got %d", len(expected), len(results))
	}
	
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("Expected %s at position %d, got %s", exp, i, results[i])
		}
	}
}

func TestLoadMultipleTimes(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	counter := 0
	
	AddIndex(func() string {
		counter++
		return "tool"
	})
	
	// 多次调用Load
	Load()
	if counter != 1 {
		t.Errorf("Expected counter to be 1 after first load, got %d", counter)
	}
	
	Load()
	if counter != 2 {
		t.Errorf("Expected counter to be 2 after second load, got %d", counter)
	}
	
	Load()
	if counter != 3 {
		t.Errorf("Expected counter to be 3 after third load, got %d", counter)
	}
}

func TestIndexWithSideEffects(t *testing.T) {
	// 清空全局状态
	PkgIndexs = []func() string{}
	
	// 模拟有副作用的索引函数
	sideEffect := false
	
	AddIndex(func() string {
		sideEffect = true
		return "tool_with_side_effect"
	})
	
	if sideEffect {
		t.Error("Side effect should not occur before Load()")
	}
	
	Load()
	
	if !sideEffect {
		t.Error("Side effect should occur after Load()")
	}
}
