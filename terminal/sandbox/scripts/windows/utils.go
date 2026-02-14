//go:build windows

package windows

import (
	"crypto/rand"
)

const passwordLen = 64
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+.;:<>?"

func randomPasswordGen() string {
	b := make([]byte, passwordLen)
	rand.Read(b)
	for i, v := range b {
		b[i] = charset[v%byte(len(charset))]
	}
	return string(b)
}
