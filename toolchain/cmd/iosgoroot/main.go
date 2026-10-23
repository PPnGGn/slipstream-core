// Command iosgoroot makes the copy of the Go toolchain that builds the iOS
// framework, see package toolchain. `make bind-ios` runs it.
//
//	go run ./toolchain/cmd/iosgoroot -src "$(go env GOROOT)" -dest .toolchain/ios-go
package main

import (
	"flag"
	"fmt"
	"os"

	"slipstream-core/toolchain"
)

func main() {
	src := flag.String("src", "", "the GOROOT to copy, `go env GOROOT`")
	dest := flag.String("dest", "", "where the copy goes")
	flag.Parse()
	if *src == "" || *dest == "" {
		fmt.Fprintln(os.Stderr, "iosgoroot: -src and -dest are both needed")
		os.Exit(2)
	}
	created, err := toolchain.Prepare(*src, *dest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "iosgoroot:", err)
		os.Exit(1)
	}
	if created {
		fmt.Fprintf(os.Stderr, "iosgoroot: %s is a copy of %s with the changes of package toolchain "+
			"(the AES and SHA instructions of iOS, the buffers of HTTP/2 streams)\n", *dest, *src)
	}
}
