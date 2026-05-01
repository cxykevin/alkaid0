package state

import "testing"

func TestStateConstants(t *testing.T) {
	// Test state values
	if StateIdle != 0 {
		t.Errorf("Expected StateIdle = 0, got %d", StateIdle)
	}
	if StateWaiting != 1 {
		t.Errorf("Expected StateWaiting = 1, got %d", StateWaiting)
	}
	if StateGeneratingPrompt != 2 {
		t.Errorf("Expected StateGeneratingPrompt = 2, got %d", StateGeneratingPrompt)
	}
	if StateRequesting != 3 {
		t.Errorf("Expected StateRequesting = 3, got %d", StateRequesting)
	}
	if StateReciving != 4 {
		t.Errorf("Expected StateReciving = 4, got %d", StateReciving)
	}
	if StateWaitApprove != 5 {
		t.Errorf("Expected StateWaitApprove = 5, got %d", StateWaitApprove)
	}
	if StateToolCalling != 6 {
		t.Errorf("Expected StateToolCalling = 6, got %d", StateToolCalling)
	}
}
