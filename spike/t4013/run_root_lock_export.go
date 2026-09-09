package t4013

import "io"

// LockRunRoot acquires the same nonblocking custody-mutation lock used by
// prepare/execute and the launcher. Closing it releases this caller's handle;
// an inherited parent handle, if any, continues to hold the lock.
func LockRunRoot(root string) (io.Closer, error) {
	lock, err := lockRunRoot(root)
	if err != nil {
		return nil, err
	}
	return lock, nil
}
