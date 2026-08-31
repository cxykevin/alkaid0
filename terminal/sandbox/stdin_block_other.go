//go:build !linux

package sandbox

// StdinBlocked cannot be detected portably on this platform.
func (c *Command) IsStdinBlocked() bool { return false }
