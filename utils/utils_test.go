package u

import (
	"errors"
	"testing"
)

func TestUnwrap(t *testing.T) {
	// Test successful unwrap
	result := Unwrap(42, nil)
	if result != 42 {
		t.Errorf("Expected 42, got %d", result)
	}

	// Test panic on error
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic, but didn't")
		}
	}()
	Unwrap(0, errors.New("test error"))
}

func TestAssert(t *testing.T) {
	// Test no panic on nil error
	Assert(nil)

	// Test panic on error
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic, but didn't")
		}
	}()
	Assert(errors.New("test error"))
}

func TestAssertB(t *testing.T) {
	// Test no panic on true
	AssertB(true)

	// Test panic on false
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic, but didn't")
		}
	}()
	AssertB(false)
}

func TestDefault(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2}

	// Test existing key
	result := Default(m, "a", 99)
	if result != 1 {
		t.Errorf("Expected 1, got %d", result)
	}

	// Test missing key
	result = Default(m, "c", 99)
	if result != 99 {
		t.Errorf("Expected 99, got %d", result)
	}
}

func TestGetH(t *testing.T) {
	h := H{"int": 42, "str": "hello"}

	// Test successful get
	val, ok := GetH[int](h, "int")
	if !ok || val != 42 {
		t.Errorf("Expected 42, true; got %d, %v", val, ok)
	}

	// Test missing key
	val, ok = GetH[int](h, "missing")
	if ok {
		t.Errorf("Expected false for missing key")
	}

}

func TestApply(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	h := H{"name": "John", "age": 30}

	result, err := Apply[TestStruct](h)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if result.Name != "John" || result.Age != 30 {
		t.Errorf("Expected {John 30}, got %+v", result)
	}
}

func TestReApply(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	ts := TestStruct{Name: "Jane", Age: 25}

	result, err := ReApply(ts)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if result["name"] != "Jane" || result["age"] != 25.0 { // json unmarshals int to float64
		t.Errorf("Expected map with name=Jane, age=25, got %v", result)
	}
}

func TestTernary(t *testing.T) {
	// Test true condition
	result := Ternary(true, "yes", "no")
	if result != "yes" {
		t.Errorf("Expected 'yes', got '%s'", result)
	}

	// Test false condition
	result = Ternary(false, "yes", "no")
	if result != "no" {
		t.Errorf("Expected 'no', got '%s'", result)
	}
}
