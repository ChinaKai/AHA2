//go:build !windows

package tray

func Run(Config) error { return ErrUnsupported }
