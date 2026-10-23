package core

import (
	"crypto/aes"
	"crypto/cipher"
	"time"
)

// MeasureAESGCM encrypts 64 KB blocks with AES-128-GCM for d and returns the
// speed in MB/s (10^6 bytes). Software AES-GCM is tens of MB/s, the AES and
// PMULL instructions of the processor several GB/s, so the figure says which of
// the two Go uses in this process. Go does not know that iOS has the
// instructions, see package toolchain: this is how the debug builds of the apps
// check that the framework was built with the copy of Go that does.
func MeasureAESGCM(d time.Duration) float64 {
	block, err := aes.NewCipher(make([]byte, 16))
	if err != nil {
		return 0
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return 0
	}
	// One nonce for every block: nothing here is secret or sent anywhere.
	nonce := make([]byte, aead.NonceSize())
	src := make([]byte, 64<<10)
	dst := make([]byte, 0, len(src)+aead.Overhead())

	var bytes int64
	start := time.Now()
	for time.Since(start) < d {
		dst = aead.Seal(dst[:0], nonce, src, nil)
		bytes += int64(len(src))
	}
	return float64(bytes) / 1e6 / time.Since(start).Seconds()
}
