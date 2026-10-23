package core

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// A session is one run of the VPN: xray, the tunnel stack on top of it and the
// helpers that go with them. StartFd and StartIO bring one up, Stop takes it
// down; the platform packages call nothing else.
//
// The parts are started and stopped one after the other, and several
// goroutines may call in (the app's main thread and a system callback, the
// iOS start and stop handlers). So both operations hold sessionMu for their
// whole length: a Stop that arrives while Start is working waits for it and
// then stops what it started, instead of running between its steps and
// leaving a tunnel up over a stopped xray.
var (
	sessionMu      sync.Mutex
	sessionRunning bool // a session is up; guarded by sessionMu
)

var errSessionRunning = errors.New("a session is already running")

// StartFd starts a session whose tunnel is the TUN descriptor fd (Android).
//
// StartFd owns fd from the call on, and the caller must not close it, whatever
// the result: on success the tunnel closes it when Stop runs, on failure
// StartFd closes it. A failed start leaves nothing running.
func StartFd(configJson string, fd int, assetDir string) error {
	handedOver := false // StartTun owns the descriptor from its call on
	err := startSession(configJson, assetDir, func() error {
		handedOver = true
		if err := StartTun(fd); err != nil {
			return err
		}
		StartMemoryReturn()
		return nil
	})
	if err != nil && !handedOver {
		CloseTunFd(fd)
	}
	return err
}

// StartIO starts a session whose tunnel reads and writes packets through rw
// (iOS). A failed start leaves nothing running.
func StartIO(configJson string, rw io.ReadWriter, assetDir string) error {
	return startSession(configJson, assetDir, func() error {
		return StartTunIO(rw)
	})
}

// Stop ends the session, if one runs. It is safe to call at any time and any
// number of times, and it stops every part even if one of them fails; the
// first error comes back.
func Stop() (err error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	defer func() { sessionRunning = false }()

	return stopParts()
}

func startSession(configJson, assetDir string, startTun func() error) (err error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()

	if sessionRunning {
		return errSessionRunning
	}

	xrayUp := false
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("start: panic recovered: %v", r)
		}
		if err != nil && xrayUp {
			_ = stopParts() // xray must not run for a tunnel that never came up
		}
		sessionRunning = err == nil
	}()

	if err = StartXray(configJson, assetDir); err != nil {
		return err
	}
	xrayUp = true
	return startTun()
}

// stopParts stops everything a session can have started, tunnel first, xray
// last. A panic in one step is turned into the error of that step; the rest
// still run.
func stopParts() (err error) {
	for _, stop := range []func() error{
		func() error { StopMemoryReturn(); return nil },
		func() error { StopTun(); return nil },
		func() error { StopTunIO(); return nil },
		StopXray,
	} {
		if e := runStop(stop); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func runStop(stop func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("stop: panic recovered: %v", r)
		}
	}()
	return stop()
}
