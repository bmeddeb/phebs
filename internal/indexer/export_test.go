package indexer

import "errors"

// ClassifyChildForTest exposes classifyChild to the external test package.
func ClassifyChildForTest(raw error, output string) error {
	return classifyChild(errors.New("wrapped: "+output), raw, output)
}
