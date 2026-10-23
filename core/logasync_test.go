package core

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// gate is a handler that holds every line until the test lets it go, standing
// for a Kotlin or Swift handler that is slow: it blocks, the way the app's
// handler could while the main thread is busy.
type gate struct {
	mu      sync.Mutex
	lines   []string
	release chan struct{}
	once    sync.Once
}

func newGate() *gate { return &gate{release: make(chan struct{})} }

func (g *gate) OnLog(level, message, source string) {
	<-g.release
	g.mu.Lock()
	g.lines = append(g.lines, level+" "+source+": "+message)
	g.mu.Unlock()
}

// open lets the lines through; calling it again does nothing.
func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

func (g *gate) got() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.lines...)
}

func useHandler(t *testing.T, h LogHandler) {
	t.Helper()
	SetLogHandler(h)
	t.Cleanup(func() { SetLogHandler(nil); flushLogs() })
}

// The lines are logged from the goroutines that carry packets and connections.
// A handler that waits (a call into Kotlin or Swift is one) must not make them
// wait: emitLog hands the line over and goes on.
func TestEmitLogDoesNotWaitForTheHandler(t *testing.T) {
	g := newGate()
	t.Cleanup(g.open)
	useHandler(t, g)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			emitLog("warning", fmt.Sprint("line ", i), "test")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitLog waits for the handler")
	}

	g.open()
	flushLogs()
	got := g.got()
	if len(got) != 100 {
		t.Fatalf("%d lines delivered, want 100", len(got))
	}
	for i, line := range got {
		if want := fmt.Sprintf("warning test: line %d", i); line != want {
			t.Fatalf("line %d = %q, want %q: the order changed", i, line, want)
		}
	}
}

// When the handler is behind by more than the queue holds, lines are dropped,
// not waited for: the traffic matters more than a log line. The loss is said
// once, with the count, when the handler is back.
func TestEmitLogDropsWhenTheQueueIsFull(t *testing.T) {
	g := newGate()
	t.Cleanup(g.open)
	useHandler(t, g)

	const sent = logQueueSize + 300
	done := make(chan struct{})
	go func() {
		for i := 0; i < sent; i++ {
			emitLog("warning", fmt.Sprint("line ", i), "test")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitLog waits for a handler that does not return")
	}

	g.open()
	flushLogs() // the handler has caught up with the queue
	emitLog("warning", "after", "test")
	flushLogs()
	out := strings.Join(g.got(), "\n")

	if !strings.Contains(out, "log lines were dropped") {
		t.Errorf("no notice about the dropped lines:\n%.400s", out)
	}
	if len(g.got()) > logQueueSize+3 {
		t.Errorf("%d lines delivered, more than the queue and the lines in flight", len(g.got()))
	}
	if !strings.Contains(out, "warning test: after") {
		t.Errorf("the line after the burst did not arrive")
	}
}

// One panicking handler call does not end the delivery of the lines after it.
func TestAPanickingHandlerDoesNotStopTheLogs(t *testing.T) {
	var mu sync.Mutex
	var got []string
	first := true
	useHandler(t, handlerFunc(func(level, message, source string) {
		mu.Lock()
		defer mu.Unlock()
		if first {
			first = false
			panic("boom")
		}
		got = append(got, message)
	}))

	emitLog("warning", "one", "test")
	emitLog("warning", "two", "test")
	flushLogs()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "two" {
		t.Errorf("delivered %v, want [two] after the panic on the first", got)
	}
}

type handlerFunc func(level, message, source string)

func (f handlerFunc) OnLog(level, message, source string) { f(level, message, source) }

// With no handler nothing is queued: a debug build logs a lot, and lines that
// nobody takes must not pile up.
func TestNoHandlerQueuesNothing(t *testing.T) {
	SetLogHandler(nil)
	flushLogs()
	for i := 0; i < logQueueSize*4; i++ {
		emitLog("info", "nobody listens", "test")
	}
	if n := len(logQueue); n != 0 {
		t.Errorf("%d lines queued with no handler", n)
	}
}

// Lines that arrive at the pace of the handler all arrive, in order.
func TestLinesArriveInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	useHandler(t, handlerFunc(func(_, message, _ string) {
		mu.Lock()
		got = append(got, message)
		mu.Unlock()
	}))

	const n = 1000
	for i := 0; i < n; i++ {
		emitLog("info", fmt.Sprint(i), "test")
		if i%100 == 99 {
			flushLogs() // stay under the size of the queue
		}
	}
	flushLogs()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("%d lines delivered, want %d", len(got), n)
	}
	for i, m := range got {
		if m != fmt.Sprint(i) {
			t.Fatalf("line %d = %q: the order changed", i, m)
		}
	}
}
