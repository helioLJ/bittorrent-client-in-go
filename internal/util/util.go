package util

import (
	"crypto/sha1"
	"math/rand"
	"time"
)

// GeneratePeerID generates a random peer ID
func GeneratePeerID() [20]byte {
	var peerID [20]byte
	rand.Read(peerID[:])
	return peerID
}

// HashInfoDict computes the SHA1 hash of the info dictionary
func HashInfoDict(info []byte) [20]byte {
	return sha1.Sum(info)
}

// RandomDuration returns a random duration between min and max
func RandomDuration(min, max time.Duration) time.Duration {
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// Min returns the smaller of two integers
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the larger of two integers
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}