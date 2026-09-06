//go:build windows

package main

import (
	"context"
	"log/slog"

	"golang.org/x/sys/windows/svc"
)

const windowsServiceName = "AHA2"

type windowsServiceHandler struct {
	options serveOptions
}

func runPlatformService(args []string) error {
	options, err := parseServeOptions("aha2 service run", args)
	if err != nil {
		return err
	}
	return svc.Run(windowsServiceName, &windowsServiceHandler{options: options})
}

func (handler *windowsServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	controls := make(chan serviceControl, 4)
	statuses := make(chan serviceStatus)
	finished := make(chan error, 1)
	go func() {
		finished <- runServiceLifecycle(context.Background(), func(ctx context.Context, ready func()) error {
			return runControlPlane(ctx, handler.options, ready)
		}, controls, statuses)
	}()
	for {
		select {
		case request, ok := <-requests:
			if !ok {
				requests = nil
				continue
			}
			switch request.Cmd {
			case svc.Stop:
				controls <- serviceStop
			case svc.Shutdown:
				controls <- serviceShutdown
			case svc.Interrogate:
				controls <- serviceInterrogate
			}
		case status := <-statuses:
			changes <- windowsServiceStatus(status)
			if status.state == serviceStopped {
				err := <-finished
				if err != nil {
					slog.Error("Windows service stopped", "error", err)
					return true, 1
				}
				return false, 0
			}
		}
	}
}

func windowsServiceStatus(status serviceStatus) svc.Status {
	result := svc.Status{CheckPoint: status.checkpoint, WaitHint: status.waitHint}
	switch status.state {
	case serviceStartPending:
		result.State = svc.StartPending
	case serviceRunning:
		result.State = svc.Running
	case serviceStopPending:
		result.State = svc.StopPending
	case serviceStopped:
		result.State = svc.Stopped
	}
	if status.acceptStop {
		result.Accepts |= svc.AcceptStop
	}
	if status.acceptDown {
		result.Accepts |= svc.AcceptShutdown
	}
	return result
}
