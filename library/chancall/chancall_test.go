package chancall

import (
	"errors"
	"testing"
	"time"
)

func TestRegisterAndCall(t *testing.T) {
	// 注册一个简单的消费者
	callFunc := Register("test_consumer", func(obj any) (any, error) {
		if str, ok := obj.(string); ok {
			return "processed: " + str, nil
		}
		return nil, errors.New("invalid input")
	})

	// 测试正常调用
	result, err := callFunc("hello")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result != "processed: hello" {
		t.Errorf("Expected 'processed: hello', got %v", result)
	}
}

func TestRegisterMultipleConsumers(t *testing.T) {
	// 注册多个消费者
	add := Register("add", func(obj any) (any, error) {
		if nums, ok := obj.([]int); ok && len(nums) == 2 {
			return nums[0] + nums[1], nil
		}
		return nil, errors.New("invalid input")
	})

	multiply := Register("multiply", func(obj any) (any, error) {
		if nums, ok := obj.([]int); ok && len(nums) == 2 {
			return nums[0] * nums[1], nil
		}
		return nil, errors.New("invalid input")
	})

	// 测试加法
	result, err := add([]int{3, 5})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result != 8 {
		t.Errorf("Expected 8, got %v", result)
	}

	// 测试乘法
	result, err = multiply([]int{3, 5})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result != 15 {
		t.Errorf("Expected 15, got %v", result)
	}
}

func TestConsumerNotFound(t *testing.T) {
	// 直接发送一个不存在的消费者事件
	ev := EventChan{
		Consumer: "non_existent_consumer",
		In:       "test",
		Out:      make(chan Ret, 1),
	}

	actChan <- ev

	// 等待结果
	select {
	case ret := <-ev.Out:
		if ret.Err == nil {
			t.Error("Expected error for non-existent consumer")
		}
		if ret.Ret != nil {
			t.Errorf("Expected nil result, got %v", ret.Ret)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestConsumerError(t *testing.T) {
	// 注册一个会返回错误的消费者
	errorFunc := Register("error_consumer", func(obj any) (any, error) {
		return nil, errors.New("intentional error")
	})

	result, err := errorFunc("test")
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if err.Error() != "intentional error" {
		t.Errorf("Expected 'intentional error', got %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
}

func TestConcurrentCalls(t *testing.T) {
	// 注册消费者
	counterFunc := Register("counter", func(obj any) (any, error) {
		if num, ok := obj.(int); ok {
			return num * 2, nil
		}
		return nil, errors.New("invalid input")
	})

	// 并发调用
	const numGoroutines = 10
	const callsPerGoroutine = 100
	results := make(chan int, numGoroutines*callsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < callsPerGoroutine; j++ {
				result, err := counterFunc(id*callsPerGoroutine + j)
				if err != nil {
					t.Errorf("Error in goroutine %d: %v", id, err)
					return
				}
				results <- result.(int)
			}
		}(i)
	}

	// 收集结果
	expectedSum := 0
	for i := 0; i < numGoroutines*callsPerGoroutine; i++ {
		expectedSum += i * 2
	}

	actualSum := 0
	for i := 0; i < numGoroutines*callsPerGoroutine; i++ {
		actualSum += <-results
	}

	if actualSum != expectedSum {
		t.Errorf("Expected sum %d, got %d", expectedSum, actualSum)
	}
}

func TestNilInput(t *testing.T) {
	// 注册一个接受nil输入的消费者
	nilFunc := Register("nil_consumer", func(obj any) (any, error) {
		if obj == nil {
			return "nil received", nil
		}
		return obj, nil
	})

	result, err := nilFunc(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result != "nil received" {
		t.Errorf("Expected 'nil received', got %v", result)
	}
}

func TestComplexDataTypes(t *testing.T) {
	type TestStruct struct {
		Name  string
		Value int
	}

	// 注册处理复杂数据类型的消费者
	structFunc := Register("struct_consumer", func(obj any) (any, error) {
		if ts, ok := obj.(TestStruct); ok {
			return TestStruct{
				Name:  ts.Name + "_processed",
				Value: ts.Value * 2,
			}, nil
		}
		return nil, errors.New("invalid struct")
	})

	input := TestStruct{Name: "test", Value: 10}
	result, err := structFunc(input)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	output, ok := result.(TestStruct)
	if !ok {
		t.Fatal("Expected TestStruct result")
	}
	if output.Name != "test_processed" {
		t.Errorf("Expected 'test_processed', got %s", output.Name)
	}
	if output.Value != 20 {
		t.Errorf("Expected 20, got %d", output.Value)
	}
}
