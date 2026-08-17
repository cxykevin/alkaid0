package openai

import (
	"testing"
	"time"
)

func TestAPIKeyLifecycle(t *testing.T) {
	key, err := NewAPIKey(1)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if len(key) != 64 {
		t.Fatalf("key length = %d, want 64", len(key))
	}
	if !ValidateAPIKey(key) {
		t.Fatal("new key should validate")
	}
	if !DeleteAPIKey(key) {
		t.Fatal("DeleteAPIKey should report an existing key")
	}
	if ValidateAPIKey(key) {
		t.Fatal("deleted key should not validate")
	}
	if DeleteAPIKey(key) {
		t.Fatal("deleting an absent key should report false")
	}
}

func TestAPIKeyExpires(t *testing.T) {
	key, err := NewAPIKey(0)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !ValidateAPIKey(key) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("zero-minute key did not expire")
}

func TestAPIKeyRejectsNegativeTimeout(t *testing.T) {
	if _, err := NewAPIKey(-1); err == nil {
		t.Fatal("negative timeout should fail")
	}
}
