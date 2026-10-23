# slipstream-core

The tunnel core of the Slipstream VPN client: [xray-core](https://github.com/XTLS/Xray-core)
for the protocols and routing, [tun2socks](https://github.com/xjasonlyu/tun2socks) to
turn the device's IP packets into connections, bound to the apps with
[gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile).

```
core/      the code shared by the platforms: session, xray, tunnel, UDP relay, config hardening
android/   the Go package bound to Kotlin (AAR); the tunnel is a TUN file descriptor
ios/       the Go package bound to Swift (xcframework); the tunnel is a packet flow
desktop/   a placeholder, not bound
фиксы.md   what was wrong, why it was dangerous, how it was fixed and measured (in Russian)
```

## Build

Requires Go (the version in `go.mod`; `GOTOOLCHAIN=auto` fetches it), `gomobile init`, the Android NDK for
Android and Xcode for iOS.

```
make bind-android   # ../slipstream/android/app/libs/slipstreamcore.aar
make bind-ios       # slipstreamcore-ios.xcframework (device and simulator)
make check          # what CI runs: gofmt, vet, go test -race, govulncheck, staticcheck
```

The bindings are stripped (`-s -w -trimpath`); `make bind-android ANDROID_TARGETS=android/arm64,android/arm`
leaves out the emulator ABIs. See the comments in the `Makefile` for the reasons and the measured sizes.

## How the apps use it

One session at a time: xray, the tunnel stack on top of it and the memory helpers. Three calls cover it, on each
platform:

| | Android (`package android`) | iOS (`package ios`) |
|---|---|---|
| start | `Start(configJson, fd, assetDir, httpSocket)` | `Start(configJson, flow, assetDir)` |
| stop | `Stop()` | `Stop()` |
| device | the TUN file descriptor `fd` | a `PacketFlow` (`ReadPacket`, `WritePacket`) |

What to rely on:

- **Threads.** `Start` blocks for as long as it takes to build the config (seconds with large geo data) and does
  not belong on the main thread. `Start` and `Stop` hold one lock: a `Stop` that arrives during a `Start` waits
  for it and then stops what it started. `Stop` is safe at any time and any number of times.
- **Failure.** A failed `Start` leaves nothing running. A `Start` while a session runs is refused.
- **The TUN descriptor (Android).** `Start` owns `fd` from the call on and closes it when it fails; the app
  detaches it from its `ParcelFileDescriptor` and must not close it either way.
- **The packet flow (iOS).** `ReadPacket` blocks and returns an error only when the flow is closed; the
  extension wakes it before `Stop`. Empty and oversize packets are skipped.
- **Config.** `configJson` is an xray config. What the core takes from it is described below.
- **Counters.** `QueryTraffic` counts the outbound tagged `proxy`. The core switches the counters on itself
  (`stats` and `policy.system.statsOutbound*`, whatever the config says); only the tag is the config's job, and
  the core logs a warning at the start when no outbound has it, since the counters would stay at zero.
- **Logs.** `SetHandler` receives the core's lines, from one goroutine of the core and in order, through a queue
  (never from the goroutines that carry traffic; when the handler falls far behind, new lines are dropped and the
  loss is reported). Release builds log warnings and errors only, with the addresses in them replaced by
  `<addr>`; `SetVerboseLogs(true)` (debug builds, before `Start`) adds the info lines and keeps the addresses.
- **Memory.** `QueryMemory` is cheap and can be polled; `FreeMemory` runs a GC and gives pages back, off the main
  thread. On iOS the extension lives under a limit of about 50 MB, see below.
- **No listening ports.** A session opens no TCP port, not even on loopback: the loopback is shared by every app on
  the phone, and a port that answers a probe (SOCKS `05 ff`, HTTP `407`) gives the client away whatever the
  password. The tunnel reaches xray inside the process (`core/bridge.go`).
- **Tunnel network.** `NewTunnelNetwork()` draws the addresses of the virtual interface for the session: a random
  /24 of 10.0.0.0/8 for the device and for the DNS address, a random unique local /64 for IPv6. Call it for every
  session and give the interface what it returns; fixed addresses are what an app that reads the interfaces of
  the phone could look for. The MTU in it is the core's (1500) and must be used as it is: a smaller one makes the
  system refuse the larger packets the stack hands back, a larger one makes the stack cut the system's.
- **Local proxy (Android).** The app is kept out of the tunnel, so its own requests need a proxy. `httpSocket`, a
  path the app can write to (short: about 100 bytes at most), makes the core serve an HTTP proxy on that Unix
  socket; `LocalHTTPProxy()` returns the socket and the password of the session. Pass an empty path for none.
  The password must not be logged. A socket file left behind by a killed process is replaced.
- **Debug commands.** `SetGCTuning`, `SetUDPTimeout`, `WriteHeapProfile`, `WriteGoroutineDump` refuse to run unless
  `SetVerboseLogs(true)` was called, i.e. in release builds.

The two packages are meant to expose the same API; a test (`ios/parity_test.go`) fails when they drift apart
anywhere but in the listed platform differences.

## The config of the provider is untrusted

Subscription providers send the xray config; the app passes it on as it is. `hardenConfig` (`core/localproxy.go`,
`core/sanitize.go`) is where it stops being trusted:

- the inbounds are replaced: by none, or, if the app asked for a socket, by one HTTP proxy on that Unix socket
  with a random password per session;
- only the top-level blocks `outbounds`, `routing`, `dns`, `policy`, `stats` and `fakedns` pass, in any
  spelling of the key (the allowlist is in `core/sanitize.go`, with the reason each other block is left out);
- inside the outbounds, `reverse` (the provider could make the phone dial out and then use it to reach the
  user's network) and the router port mapping of `finalmask` are removed.

The tests start a real xray with the hostile configs and check that nothing listens (the OS is asked what the
process listens on, `lsof`), nothing is written and the environment is untouched. `фиксы.md` has the story of each rule.

## Memory on iOS

The Network Extension is killed above about 50 MB. What the core does about it: the UDP sessions relay with 8 KB
buffers (`core/udprelay.go`; tun2socks' own 64 KB per direction filled the extension under a browsing load), GC
at 20 percent growth, a loop that hands free memory back to the OS every two minutes, and an idle UDP session
that is let go after 15 s on iOS (`iosUDPTimeout`; 60 s on Android, where memory is not so scarce). How it was
measured (debug-only sampler in the app's extension, files read through `devicectl`) is in `фиксы.md`, steps 6
and 11г.

## Checks and CI

`make check` runs everything; `.github/workflows/ci.yml` runs it on every push, pull request and once a day (new
vulnerabilities appear without a change here). `make check-xray-pin` compares the xray-core pin with the one of the
rules repository (`RULES_DIR`, `../slipstream-georules` by default) before a release.
