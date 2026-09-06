//go:build !windows

package main

import "fmt"

func runPlatformService(_ []string) error {
	return fmt.Errorf("aha2 service run is only supported on Windows")
}
