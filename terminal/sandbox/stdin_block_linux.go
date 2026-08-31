//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"strings"
)

// StdinBlocked reports whether a process is sleeping inside a terminal read.
func (c *Command) IsStdinBlocked() bool {
	pid := c.PID()
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", pid))
	if err != nil {
		return false
	}
	wchan := strings.ToLower(strings.TrimSpace(string(data)))
	return strings.Contains(wchan, "n_tty_read") || strings.Contains(wchan, "tty_read")
}
