package stack

import "testing"

func TestStack(t *testing.T) {
	s := New()
	if !s.IsEmpty() {
		t.Errorf("Expected empty stack")
	}
	if s.Size() != 0 {
		t.Errorf("Expected size 0, got %d", s.Size())
	}

	s.Push(1)
	s.Push("two")
	if s.IsEmpty() {
		t.Errorf("Expected non-empty stack")
	}
	if s.Size() != 2 {
		t.Errorf("Expected size 2, got %d", s.Size())
	}

	if val, ok := s.Top(); !ok || val != "two" {
		t.Errorf("Expected top 'two', got %v", val)
	}

	if val, ok := s.Bottom(); !ok || val != 1 {
		t.Errorf("Expected bottom 1, got %v", val)
	}

	if val, ok := s.Pop(); !ok || val != "two" {
		t.Errorf("Expected pop 'two', got %v", val)
	}

	if val, ok := s.Pop(); !ok || val != 1 {
		t.Errorf("Expected pop 1, got %v", val)
	}

	if _, ok := s.Pop(); ok {
		t.Errorf("Expected pop to fail on empty stack")
	}

	if _, ok := s.Top(); ok {
		t.Errorf("Expected top to fail on empty stack")
	}

	if _, ok := s.Bottom(); ok {
		t.Errorf("Expected bottom to fail on empty stack")
	}
}
