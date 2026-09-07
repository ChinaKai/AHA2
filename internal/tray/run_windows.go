//go:build windows

package tray

import (
	"fmt"
	"os"
)

func Run(config Config) error {
	command, err := BuildServerCommand(config)
	if err != nil {
		return err
	}
	if info, err := os.Stat(command.Path); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("path is a directory")
		}
		return fmt.Errorf("AHA2 server executable is unavailable: %w", err)
	}
	lock, err := acquireSingleInstance()
	if err != nil {
		return err
	}
	defer lock.Close()
	hideConsoleWindow()

	supervisor := NewSupervisor(OSLauncher{}, command, DefaultRetryPolicy())
	supervisor.Run()
	if err := supervisor.Start(); err != nil {
		return err
	}
	if err := runNativeTray(supervisor, config); err != nil {
		_ = supervisor.Exit()
		<-supervisor.Done()
		return err
	}
	select {
	case <-supervisor.Done():
	default:
		_ = supervisor.Exit()
		<-supervisor.Done()
	}
	return nil
}
