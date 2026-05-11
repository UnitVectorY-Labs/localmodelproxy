package main

import (
	"runtime/debug"
)

var Version = "dev" // This will be set by the build systems to the release version

// main is the entry point for the ghorgsync command-line tool.
func main() {
	// Set the build version from the build info if not set by the build system
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				Version = bi.Main.Version
			}
		}
	}

	// TODO: Implement everything
}
