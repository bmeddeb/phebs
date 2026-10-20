// Command go-reference-helper owns one fixed protected reference transaction.
package main

import (
	"os"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() { os.Exit(t421.LinuxGoReferenceHelperMain()) }
