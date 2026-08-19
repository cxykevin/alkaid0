package apikey

import (
	"testing"
	"time"
)

func TestNewAndValidate(t *testing.T) {
	key, err := New(1)
	if err != nil {
		t.Fatalf("New(1) failed: %v", err)
	}
	if key == "" {
		t.Fatal("New(1) returned empty key")
	}
	if len(key) != 64 { // 32 bytes -> 64 hex chars
		t.Errorf("expected 64 hex chars, got %d", len(key))
	}

	if !Validate(key) {
		t.Error("Validate(key) should return true for valid key")
	}
}

func TestNewNegativeTimeout(t *testing.T) {
	_, err := New(-1)
	if err == nil {
		t.Fatal("New(-1) should return error")
	}
}

func TestValidateExpiredKey(t *testing.T) {
	key, err := New(0)
	if err != nil {
		t.Fatalf("New(0) failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if Validate(key) {
		t.Error("Validate should return false for expired key")
	}
}

func TestDelete(t *testing.T) {
	key, err := New(10)
	if err != nil {
		t.Fatalf("New(10) failed: %v", err)
	}

	if !Delete(key) {
		t.Error("Delete(key) should return true for existing key")
	}

	if Validate(key) {
		t.Error("Validate should return false after Delete")
	}

	if Delete(key) {
		t.Error("Delete should return false for already deleted key")
	}
}

func TestValidateNonExistentKey(t *testing.T) {
	if Validate("nonexistent") {
		t.Error("Validate should return false for non-existent key")
	}
}
