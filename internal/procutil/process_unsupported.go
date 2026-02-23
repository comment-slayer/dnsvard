//go:build !darwin && !linux

package procutil

func Running(int) bool {
	return false
}
