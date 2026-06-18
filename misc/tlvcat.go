//go:build ignore

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/alayacore/alayacore/internal/stream"
)

func main() {
	for {
		tag, value, err := stream.ReadTLV(os.Stdin)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if _, err := fmt.Printf("%s\t%s\n", tag, value); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(1)
		}
	}
}
