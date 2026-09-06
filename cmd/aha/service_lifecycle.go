package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

type serviceControl uint8

const (
	serviceInterrogate serviceControl = iota
	serviceStop
	serviceShutdown
)

type serviceState uint8

const (
	serviceStartPending serviceState = iota
	serviceRunning
	serviceStopPending
	serviceStopped
)

type serviceStatus struct {
	state      serviceState
	acceptStop bool
	acceptDown bool
	checkpoint uint32
	waitHint   uint32
}

type serviceRunFunc func(context.Context, func()) error

func runServiceLifecycle(parent context.Context, run serviceRunFunc, controls <-chan serviceControl, statuses chan<- serviceStatus) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ready := make(chan struct{})
	var readyOnce sync.Once
	done := make(chan error, 1)
	statuses <- serviceStatus{state: serviceStartPending, checkpoint: 1, waitHint: 30_000}
	go func() {
		done <- run(ctx, func() { readyOnce.Do(func() { close(ready) }) })
	}()
	stop := func() error {
		statuses <- serviceStatus{state: serviceStopPending, checkpoint: 1, waitHint: 15_000}
		cancel()
		err := <-done
		statuses <- serviceStatus{state: serviceStopped}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	starting := serviceStatus{state: serviceStartPending, checkpoint: 1, waitHint: 30_000}
	startupTicker := time.NewTicker(5 * time.Second)
	defer startupTicker.Stop()
	for {
		select {
		case <-ready:
			startupTicker.Stop()
			goto running
		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if control == serviceStop || control == serviceShutdown {
				return stop()
			}
			if control == serviceInterrogate {
				statuses <- starting
			}
		case err := <-done:
			statuses <- serviceStatus{state: serviceStopped}
			return err
		case <-parent.Done():
			return stop()
		case <-startupTicker.C:
			starting.checkpoint++
			statuses <- starting
		}
	}

running:
	current := serviceStatus{state: serviceRunning, acceptStop: true, acceptDown: true}
	statuses <- current
	for {
		select {
		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			switch control {
			case serviceStop, serviceShutdown:
				return stop()
			case serviceInterrogate:
				statuses <- current
			}
		case err := <-done:
			statuses <- serviceStatus{state: serviceStopPending, checkpoint: 1, waitHint: 15_000}
			statuses <- serviceStatus{state: serviceStopped}
			return err
		case <-parent.Done():
			return stop()
		}
	}
}
