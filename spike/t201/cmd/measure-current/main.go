// Command measure-current reports today's extractor behavior on the T20.1
// neutral corpus. It neither runs an indexer nor modifies the corpus.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t201"
)

func main() {
	profileName := t201.SmallProfileName
	if len(os.Args) == 2 {
		profileName = os.Args[1]
	}
	profile, err := t201.GenerateProfile(profileName)
	if err != nil {
		panic(err)
	}
	measurements, err := t201.MeasureCurrentExtraction(context.Background(), profile)
	if err != nil {
		panic(err)
	}
	encoded, err := json.MarshalIndent(measurements, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))
}
