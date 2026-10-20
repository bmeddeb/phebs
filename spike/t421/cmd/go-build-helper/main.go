// Command go-build-helper is a fixed private Linux neutral-build endpoint.
package main

import (
	"os"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() { os.Exit(t421.LinuxGoBuildHelperMain()) }
