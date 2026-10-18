// Command input-tree-helper is the fixed private Linux construction endpoint.
// It accepts no public paths, commands or flags; its parent owns inherited FDs.
package main

import (
	"os"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() { os.Exit(t421.LinuxInputTreeHelperMain()) }
