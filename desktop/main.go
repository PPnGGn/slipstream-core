// Command desktop is a placeholder entry point for a future desktop build
// (Windows/macOS/Linux) of the same tunnel core used by ../mobile. Not
// wired into any app yet — kept as a stub so the package layout already
// has a slot for it instead of forcing another reshuffle later.
package main

import (
	"fmt"
	"os"

	v2core "v2net-core/core"
)

func main() {
	fmt.Println("v2net-core desktop entry point is not implemented yet.")
	_ = v2core.OutboundTag
	os.Exit(1)
}
