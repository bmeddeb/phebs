package storeaccounting

import (
	"context"
	"net"
	"os"
)

// adopt closes the input on every path. FileConn's owned pollable duplicate is
// CLOEXEC; changing shared nonblocking flags is not an immutable-OFD claim.
func adopt(file *os.File) (*net.UnixConn, error) {
	if file == nil {
		return nil, ErrConfig
	}
	if err := protectInheritance(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	conn, err := net.FileConn(file)
	closeErr := file.Close()
	if err != nil {
		return nil, ErrTransport
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok || closeErr != nil {
		_ = conn.Close()
		return nil, ErrTransport
	}
	return unix, nil
}

func closeOnCancel(ctx context.Context, conn *net.UnixConn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}
}
