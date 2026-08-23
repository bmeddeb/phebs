// Command t4013-inspect performs bounded exact ceremony-control inspection.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	planSchema := flag.String("plan-schema", "", "plan whose exact schema is printed")
	planDigest := flag.String("plan-digest", "", "plan whose exact digest is printed")
	jsonValue := flag.String("json-value", "", "canonical ceremony JSON control")
	key := flag.String("key", "", "string key printed from -json-value")
	checksums := flag.String("checksums", "", "canonical source-free checksum inventory")
	exactDirectory := flag.String("exact-directory", "", "directory requiring exactly the remaining basenames")
	directory := flag.String("directory", "", "directory inspected for a forbidden prefix")
	forbidPrefix := flag.String("forbid-prefix", "", "entry prefix refused beneath -directory")
	maximumEntries := flag.Int("maximum-entries", -1, "maximum entries read beneath -directory")
	fileDigest := flag.String("file-digest", "", "regular control whose exact digest is printed")
	maximumBytes := flag.Int("maximum-bytes", 0, "byte bound for -file-digest")
	flag.Parse()

	modes := 0
	for _, selected := range []bool{
		*planSchema != "", *planDigest != "", *jsonValue != "", *checksums != "",
		*exactDirectory != "", *directory != "", *fileDigest != "",
	} {
		if selected {
			modes++
		}
	}
	if modes != 1 {
		fail("exactly one inspection mode is required")
	}

	switch {
	case *planSchema != "" || *planDigest != "":
		if flag.NArg() != 0 {
			fail("plan inspection accepts no positional arguments")
		}
		path := *planSchema
		if path == "" {
			path = *planDigest
		}
		schema, digest, err := t4013.InspectPlanControl(path)
		if err != nil {
			fail("inspect plan: %v", err)
		}
		if *planSchema != "" {
			fmt.Println(schema)
		} else {
			fmt.Println(digest)
		}
	case *jsonValue != "":
		if flag.NArg() != 0 || *key == "" {
			fail("-json-value requires -key and no positional arguments")
		}
		value, err := t4013.InspectCeremonyJSONValue(*jsonValue, *key)
		if err != nil {
			fail("inspect ceremony JSON: %v", err)
		}
		fmt.Println(value)
	case *checksums != "":
		if flag.NArg() != 0 {
			fail("checksum inspection accepts no positional arguments")
		}
		if err := t4013.InspectChecksumInventory(*checksums); err != nil {
			fail("inspect checksums: %v", err)
		}
	case *exactDirectory != "":
		if err := t4013.InspectExactDirectory(*exactDirectory, flag.Args()); err != nil {
			fail("inspect exact directory: %v", err)
		}
	case *directory != "":
		if flag.NArg() != 0 || *forbidPrefix == "" || *maximumEntries <= 0 {
			fail("-directory requires -forbid-prefix, -maximum-entries, and no positional arguments")
		}
		if err := t4013.InspectDirectoryPrefixAbsent(
			*directory, *forbidPrefix, *maximumEntries,
		); err != nil {
			fail("inspect bounded directory: %v", err)
		}
	case *fileDigest != "":
		if flag.NArg() != 0 || *maximumBytes <= 0 {
			fail("-file-digest requires -maximum-bytes and no positional arguments")
		}
		digest, err := t4013.InspectExactFileDigest(*fileDigest, *maximumBytes)
		if err != nil {
			fail("inspect exact file: %v", err)
		}
		fmt.Println(digest)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-inspect: "+format+"\n", args...)
	os.Exit(1)
}
