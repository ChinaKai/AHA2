//go:build windows

package tray

import (
	"fmt"
	"os"
)

func Run(config Config) error {
	// The stored address wins over the one baked into the scheduled task, so a
	// change made in the UI survives a restart.
	config.Listen = EffectiveListen(OSCommandRunner{}, config)
	command, err := BuildServerCommand(config)
	if err != nil {
		logTrayFailure(config.DataDir, err)
		return err
	}
	if info, err := os.Stat(command.Path); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("path is a directory")
		}
		err = fmt.Errorf("AHA2 server executable is unavailable: %w", err)
		logTrayFailure(config.DataDir, err)
		return err
	}
	lock, err := acquireSingleInstance()
	if err != nil {
		return err
	}
	defer lock.Close()
	hideConsoleWindow()

	supervisor := NewSupervisor(OSLauncher{LogPath: ServerLogPath(config.DataDir)}, command, DefaultRetryPolicy())
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

// logTrayFailure persists startup failures that happen before the supervisor
// exists. The tray runs without a console, so stderr alone would lose them.
func logTrayFailure(dataDir string, err error) {
	log, openErr := openServerLog(ServerLogPath(dataDir), 0)
	if openErr != nil {
		return
	}
	log.writeEntry("AHA2 tray failed to start server: %v", err)
	_ = log.Close()
}
