package core

import "time"

// loop calls fn every interval on its own goroutine until stop. The debug
// memory log runs on one.
type loop struct {
	quit chan struct{} // closed by stop: tells the goroutine to return
	done chan struct{} // closed by the goroutine on its way out
}

func startLoop(interval time.Duration, fn func()) *loop {
	l := &loop{quit: make(chan struct{}), done: make(chan struct{})}
	go l.run(interval, fn)
	return l
}

func (l *loop) run(interval time.Duration, fn func()) {
	defer close(l.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fn()
		case <-l.quit:
			return
		}
	}
}

// stop ends the loop and waits until it has, so fn never runs after stop
// returns. Call it once: closing quit twice panics.
func (l *loop) stop() {
	close(l.quit)
	<-l.done
}
