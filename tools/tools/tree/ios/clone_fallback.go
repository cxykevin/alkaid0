//go:build !linux && !windows && !darwin

package ios

import "fmt"

func cloneFile(_, _ int) error {
	return fmt.Errorf("clone not supported")
}
