package loop

import (
	"errors"
	"testing"
	"time"
)

func TestRetryBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, baseBackoff},
		{1, baseBackoff},
		{2, 2 * baseBackoff},
		{3, 4 * baseBackoff},
		{10, 512 * baseBackoff},
	}
	for _, c := range cases {
		if got := retryBackoff(c.attempt); got != c.want {
			t.Fatalf("retryBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
	// 极大 attempt 不能溢出成负数（否则会退化成立即重试）。
	if got := retryBackoff(1 << 20); got <= 0 {
		t.Fatalf("retryBackoff must stay positive, got %v", got)
	}
}

func TestIsFatalRetryError(t *testing.T) {
	fatal := []string{
		"API error: 402 Insufficient Balance",
		"quota exceeded for this billing period",
		"账户余额不足",
		"credits exhausted",
		"payment required",
	}
	for _, msg := range fatal {
		if !isFatalRetryError(errors.New(msg)) {
			t.Fatalf("expected fatal: %q", msg)
		}
	}
	nonFatal := []string{
		"HTTP 500",
		"HTTP 429 Too Many Requests",
		"dial tcp: connection refused",
		"unexpected eof",
	}
	for _, msg := range nonFatal {
		if isFatalRetryError(errors.New(msg)) {
			t.Fatalf("did not expect fatal: %q", msg)
		}
	}
	if isFatalRetryError(nil) {
		t.Fatal("nil error must not be fatal")
	}
}
