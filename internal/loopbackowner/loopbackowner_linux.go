//go:build linux

package loopbackowner

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tcpListen is the kernel's TCP_LISTEN state in /proc/net/tcp{,6}.
const tcpListen = "0A"

func check(pid int, addr string) error {
	if pid <= 0 {
		return fmt.Errorf("loopback owner: invalid pid %d", pid)
	}
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("loopback owner: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("loopback owner: invalid port %q", portText)
	}
	// The pid's own view names the network namespace it actually binds in.
	root := filepath.Join("/proc", strconv.Itoa(pid))
	listeners, err := portListeners(root, uint16(port))
	if errors.Is(err, os.ErrNotExist) {
		// The pid has exited (a zombie has no net view either) and holds no
		// sockets. Seen from this process's namespace, which its children
		// share, any listener left on the port belongs to another process.
		if listeners, err = portListeners("/proc/self", uint16(port)); err != nil {
			return err
		}
		if len(listeners) != 0 {
			return fmt.Errorf("%w (pid %d exited)", ErrForeign, pid)
		}
		return fmt.Errorf("%w: pid %d", ErrExited, pid)
	}
	if err != nil {
		return err
	}
	if len(listeners) == 0 {
		return ErrNotListening
	}
	entries, err := os.ReadDir(filepath.Join(root, "fd"))
	if err != nil {
		return fmt.Errorf("loopback owner: read descriptors of pid %d: %w", pid, err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(root, "fd", entry.Name()))
		if err != nil {
			continue // Closed between ReadDir and Readlink.
		}
		inode, ok := strings.CutPrefix(target, "socket:[")
		if ok {
			delete(listeners, strings.TrimSuffix(inode, "]"))
		}
	}
	if len(listeners) != 0 {
		return ErrForeign
	}
	return nil
}

// portListeners returns the socket inodes of every TCP listener on port in
// the network namespace seen through the /proc entry root.
func portListeners(root string, port uint16) (map[string]bool, error) {
	listeners := map[string]bool{}
	for _, table := range []string{"tcp", "tcp6"} {
		if err := listenInodes(filepath.Join(root, "net", table), port, listeners); err != nil {
			return nil, err
		}
	}
	return listeners, nil
}

// listenInodes adds the socket inode of every LISTEN entry on port in one
// /proc/net/tcp-format table. A missing tcp6 table (IPv6 disabled) is empty.
func listenInodes(path string, port uint16, into map[string]bool) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && filepath.Base(path) == "tcp6" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loopback owner: %w", err)
	}
	defer func() { _ = file.Close() }()
	want := fmt.Sprintf(":%04X", port)
	scanner := bufio.NewScanner(file)
	scanner.Scan() // Header.
	for scanner.Scan() {
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			return fmt.Errorf("loopback owner: malformed %s row", path)
		}
		if fields[3] == tcpListen && strings.HasSuffix(fields[1], want) {
			into[fields[9]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("loopback owner: %w", err)
	}
	return nil
}
