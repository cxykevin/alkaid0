// Package openai contains the OpenAI-compatible server support code.
package openai

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const apiKeyBytes = 32

type apiKeyEntry struct {
	expiresAt time.Time
	timer     *time.Timer
}

var (
	apiKeysMu sync.RWMutex
	apiKeys   = make(map[string]*apiKeyEntry)
)

// NewAPIKey creates an in-memory API key that remains valid for timeoutMinutes
// minutes from creation. A timeout of zero creates an immediately expired key.
func NewAPIKey(timeoutMinutes int) (string, error) {
	if timeoutMinutes < 0 {
		return "", errors.New("API key timeout must not be negative")
	}

	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	expiresAt := time.Now().Add(time.Duration(timeoutMinutes) * time.Minute)

	apiKeysMu.Lock()
	entry := &apiKeyEntry{expiresAt: expiresAt}
	apiKeys[key] = entry
	// 在同一把锁下安装 timer，避免 DeleteAPIKey 并发读取未初始化的 timer。
	entry.timer = time.AfterFunc(time.Until(expiresAt), func() {
		apiKeysMu.Lock()
		if current, ok := apiKeys[key]; ok && current == entry {
			delete(apiKeys, key)
		}
		apiKeysMu.Unlock()
	})
	apiKeysMu.Unlock()
	return key, nil
}

// DeleteAPIKey revokes key and reports whether it was present.
func DeleteAPIKey(key string) bool {
	apiKeysMu.Lock()
	defer apiKeysMu.Unlock()

	entry, ok := apiKeys[key]
	if !ok {
		return false
	}
	delete(apiKeys, key)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	return true
}

// ValidateAPIKey reports whether key exists and has not reached its fixed
// expiration time. Expired keys are removed as they are observed.
func ValidateAPIKey(key string) bool {
	now := time.Now()

	apiKeysMu.Lock()
	defer apiKeysMu.Unlock()

	entry, ok := apiKeys[key]
	if !ok {
		return false
	}
	expiresAt := entry.expiresAt
	if !now.Before(expiresAt) {
		delete(apiKeys, key)
		if entry.timer != nil {
			entry.timer.Stop()
		}
		return false
	}
	return true
}
