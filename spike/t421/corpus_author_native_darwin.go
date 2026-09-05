//go:build darwin

package t421

import (
	"os"
	"syscall"
)

func corpusAuthorNativeIdentity(info os.FileInfo, volume [2]int32) (ExecutionCorpusSourceIdentity, error) {
	if info == nil || !info.IsDir() || !inputCustodyOwned(info) || info.Mode().Perm() != 0o700 {
		return ExecutionCorpusSourceIdentity{}, ErrExecutionCorpusAuthor
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || stat.Ino == 0 {
		return ExecutionCorpusSourceIdentity{}, ErrExecutionCorpusAuthor
	}
	return ExecutionCorpusSourceIdentity{Device: stat.Dev, Inode: stat.Ino, Generation: stat.Gen, Volume: volume}, nil
}
