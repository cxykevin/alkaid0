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
	// 注册一个消费者
	counter := Register("counter", func(obj any) (any, error) {
		if n, ok := obj.(int); ok {
			time.Sleep(10 * time.Millisecond) // 模拟一些处理时间
			return n * 2, nil
		}
		return nil, errors.New("invalid input")
	})

	// 并发调用
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			result, err := counter(n)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if result != n*2 {
				t.Errorf("Expected %d, got %v", n*2, result)
			}
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for concurrent calls")
		}
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
