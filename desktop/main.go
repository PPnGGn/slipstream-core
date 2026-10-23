// Command desktop is a placeholder entry point for a future desktop build
// (Windows/macOS/Linux) of the same tunnel core used by ../android and ../ios.
// Not wired into any app yet — kept as a stub so the package layout already
// has a slot for it instead of forcing another reshuffle later. Start a
// session with core.StartFd or core.StartIO and end it with core.Stop; the
// steps they are made of are not exported.
package main

import (
	"fmt"
	"os"

	slipcore "slipstream-core/core"
)

func main() {
	fmt.Println("slipstream-core desktop entry point is not implemented yet.")
	_ = slipcore.OutboundTag
	os.Exit(1)
}
