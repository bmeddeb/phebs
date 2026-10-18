// Command observe reports bounded aggregate native process-tree samples for
// the T50.3 local operating measurement. It creates no product evidence.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 64), 64)
	output := json.NewEncoder(os.Stdout)
	for input.Scan() {
		pid, err := strconv.Atoi(input.Text())
		if err != nil || pid <= 0 {
			fmt.Fprintln(os.Stderr, "invalid native observation root")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		observation, err := t4013.ObserveProcessTree(ctx, pid)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if encodeErr := output.Encode(map[string]bool{"unavailable": true}); encodeErr != nil {
				os.Exit(1)
			}
			os.Exit(1)
		}
		if err := output.Encode(map[string]any{
			"rss_bytes": observation.RSSBytes, "descendants": observation.Descendants,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "write native aggregate observation")
			os.Exit(1)
		}
	}
	if err := input.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read native observation root")
		os.Exit(1)
	}
}
