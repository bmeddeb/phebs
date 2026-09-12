package t421

import "encoding/binary"

// executionHostObservation retains actual prework facts without issuing profile
// or freeze admission. FSIDs are private; returned source-free identities use
// the explicitly defined v1 encoding below, never native struct memory.
type executionHostObservation struct {
	Host  ExecutionHost
	FSIDs [3][2]int32 // backing, actual data root, actual ballast inode
}

func executionFSIDIdentity(fsid [2]int32) string {
	var encoded [8]byte
	binary.BigEndian.PutUint32(encoded[:4], uint32(fsid[0]))
	binary.BigEndian.PutUint32(encoded[4:], uint32(fsid[1]))
	return SHA256(encoded[:])
}
