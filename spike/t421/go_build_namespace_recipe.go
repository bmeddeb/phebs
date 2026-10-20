package t421

// This private neutral build recipe is neither a ceremony plan nor reference
// provenance for the SDK/source/modules subsequently needed by the Go owner.
const (
	linuxGoBuildRecipe    = "linux-go-build-namespace-v1"
	linuxGoBuildRole      = "t42-go-build-helper"
	linuxGoBuildModule    = "module neutral.invalid/protected\n\ngo 1.26.5\n"
	linuxGoBuildSource    = "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"neutral protected build\") }\n"
	linuxGoBuildGitConfig = "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n"
)
