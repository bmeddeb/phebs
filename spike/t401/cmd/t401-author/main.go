package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	t401 "github.com/bmeddeb/phebs/spike/t401"
)

func main() {
	profileName := flag.String("profile", "", "profile kind: structural or semantic")
	scale := flag.String("scale", "frozen", "profile scale: frozen or projection")
	moduleRoot := flag.String("module-root", "", "absolute phebs module root (output exclusion authority)")
	output := flag.String("output", "", "absolute output directory")
	confirm := flag.Bool("confirm-frozen", false, "explicitly authorize giant frozen-corpus authoring")
	timeout := flag.Duration("timeout", 24*time.Hour, "bounded authoring timeout")
	flag.Parse()
	if *profileName != "structural" && *profileName != "semantic" {
		fmt.Fprintln(os.Stderr, "-profile must be structural or semantic")
		os.Exit(2)
	}
	var selected t401.Profile
	var err error
	switch *scale {
	case "frozen":
		profiles, profilesErr := t401.FrozenProfiles()
		if profilesErr != nil {
			err = profilesErr
			break
		}
		for _, profile := range profiles {
			if profile.Kind == *profileName {
				selected = profile
				break
			}
		}
	case "projection":
		selected, err = t401.ProjectionProfile(*profileName)
	default:
		fmt.Fprintln(os.Stderr, "-scale must be frozen or projection")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	receipt, err := t401.Author(ctx, t401.AuthorRequest{
		ModuleRoot: *moduleRoot, Output: *output, Profile: selected, ConfirmFrozen: *confirm,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := t401.MarshalCanonical(receipt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
