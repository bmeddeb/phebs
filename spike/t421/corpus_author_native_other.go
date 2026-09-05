//go:build !darwin

package t421

import "os"

func corpusAuthorNativeIdentity(os.FileInfo, [2]int32) (ExecutionCorpusSourceIdentity, error) {
	return ExecutionCorpusSourceIdentity{}, ErrExecutionCorpusAuthor
}
