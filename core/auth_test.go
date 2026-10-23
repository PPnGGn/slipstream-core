package core

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listeningTCP returns the TCP addresses this process listens on, as the OS
// reports them (IPv4 and IPv6, wildcard or loopback).
//
// A scan of all 65535 ports, the way an app on the phone would find them, would
// be the closer copy of the threat, but on macOS two scans in a row leave the
// kernel out of socket buffers for seconds (ENOBUFS on connect and on listen),
// which makes the scan miss ports and breaks the tests that follow. Asking the
// OS what the process listens on gives the same answer without that.
func listeningTCP(t *testing.T) []string {
	t.Helper()
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		t.Skip("lsof is not installed")
	}
	out, err := exec.Command(lsof, "-nP", "-a", "-p", fmt.Sprint(os.Getpid()), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	if err != nil && len(out) == 0 {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil // lsof exits with 1 when it finds nothing
		}
		t.Fatalf("lsof: %v", err)
	}
	var addrs []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			addrs = append(addrs, line[1:])
		}
	}
	return addrs
}

// assertStartOpensNoTCPListener runs start and checks that the process listens
// on no TCP port while the session runs. Excluding apps from the VPN does not
// hide a listening port from them: the loopback is shared, only not listening
// does.
//
// A check that finds nothing proves nothing unless it can find something, so a
// listener of the test is opened in between and has to show up.
func assertStartOpensNoTCPListener(t *testing.T, start func() error) {
	t.Helper()
	if before := listeningTCP(t); len(before) != 0 {
		t.Fatalf("the test process already listens on %v", before)
	}

	if err := start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if during := listeningTCP(t); len(during) != 0 {
		t.Errorf("the session listens on %v: any app on the phone can connect to it", during)
	}

	canary, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer canary.Close()
	for _, addr := range listeningTCP(t) {
		if addr == canary.Addr().String() {
			return
		}
	}
	t.Fatalf("listeningTCP() does not list the listener on %v: the check is blind", canary.Addr())
}

func TestASessionOpensNoTCPListener(t *testing.T) {
	t.Run("fd", func(t *testing.T) {
		useFileDevices(t)
		t.Cleanup(func() { _ = Stop() })
		a, _ := socketPair(t)
		assertStartOpensNoTCPListener(t, func() error { return StartFd(hardenSample, a, "", "") })
	})
	t.Run("io", func(t *testing.T) {
		t.Cleanup(func() { _ = Stop() })
		assertStartOpensNoTCPListener(t, func() error { return StartIO(hardenSample, eofReadWriter{}, "") })
	})
}

// Even with the HTTP proxy of the app asked for, the session still listens on
// no port: the proxy is on a Unix socket.
func TestTheHTTPProxyOpensNoTCPListener(t *testing.T) {
	sock := shortSocketPath(t)
	t.Cleanup(func() { _ = stopXray() })
	assertStartOpensNoTCPListener(t, func() error { return startXray(hardenSample, "", sock) })
}

// shortSocketPath returns a socket path short enough for sockaddr_un (104 bytes
// on macOS), which t.TempDir() often is not.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

const freedomConfig = `{"outbounds": [{"protocol": "freedom", "tag": "direct"}]}`

func dialSocket(t *testing.T, path string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c
}

// The app's own requests use an HTTP proxy that listens on a Unix socket, which
// no TCP scan reaches, and that still wants the password of the session.
func TestHTTPProxyOnASocketNeedsThePassword(t *testing.T) {
	sock := shortSocketPath(t)
	if err := startXray(freedomConfig, "", sock); err != nil {
		t.Fatalf("startXray: %v", err)
	}
	t.Cleanup(func() { _ = stopXray() })
	path, user, pass, ok := ActiveHTTPProxy()
	if !ok || path != sock {
		t.Fatalf("ActiveHTTPProxy() = %q, %v; want the socket %q", path, ok, sock)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "through the tunnel")
	}))
	defer target.Close()
	request := func(auth string) *http.Response {
		c := dialSocket(t, sock)
		req := fmt.Sprintf("GET %s/ HTTP/1.1\r\nHost: %s\r\n%s\r\n", target.URL, target.Listener.Addr(), auth)
		if _, err := fmt.Fprint(c, req); err != nil {
			t.Fatalf("write: %v", err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if got := request("").StatusCode; got != http.StatusProxyAuthRequired {
		t.Errorf("without credentials: status %d, want 407", got)
	}
	wrong := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":wrong")) + "\r\n"
	if got := request(wrong).StatusCode; got != http.StatusProxyAuthRequired {
		t.Errorf("with a wrong password: status %d, want 407", got)
	}
	right := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) + "\r\n"
	if got := request(right).StatusCode; got != http.StatusOK {
		t.Errorf("with the session password: status %d, want 200", got)
	}

	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm()&0o002 != 0 {
		t.Errorf("socket mode %v (%v): must not be writable by others", fi.Mode(), err)
	}
}

// The path comes from the app, but a wrong one must cost a failed start, not
// a file: only a socket is ever deleted.
func TestAFileThatIsNotASocketIsNotDeleted(t *testing.T) {
	path := shortSocketPath(t)
	if err := os.WriteFile(path, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := startXray(freedomConfig, "", path); err == nil {
		_ = stopXray()
		t.Fatal("startXray succeeded with a regular file at the socket path")
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "precious" {
		t.Errorf("the file was lost or changed: %q, %v", got, err)
	}
}

// staleSocket leaves a socket file at path with nobody listening on it, what a
// process that was killed leaves behind.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if fi, err := os.Lstat(path); err != nil || fi.Mode().Type() != os.ModeSocket {
		t.Fatalf("no stale socket at %s: %v", path, err)
	}
}

// The socket file does not outlive the session, and a file left behind by a
// process that was killed does not stop the next session from starting.
func TestHTTPSocketIsCleanedUpAndReplaced(t *testing.T) {
	sock := shortSocketPath(t)
	staleSocket(t, sock)

	if err := startXray(freedomConfig, "", sock); err != nil {
		t.Fatalf("startXray over a stale socket: %v", err)
	}
	_ = dialSocket(t, sock)

	if err := stopXray(); err != nil {
		t.Fatalf("stopXray: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("the socket file is still there after stopXray: %v", err)
	}
	if _, _, _, ok := ActiveHTTPProxy(); ok {
		t.Error("the credentials outlived the session")
	}
}
