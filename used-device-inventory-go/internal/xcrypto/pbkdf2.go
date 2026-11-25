// Package xcrypto holds the key-derivation primitives the password
// hashes need. They are implemented here, from the public algorithm
// definitions, so the module needs no dependency beyond the standard
// library and the SQLite driver.
package xcrypto

import (
	"crypto/hmac"
	"encoding/binary"
	"hash"
)

// PBKDF2 derives a key with PBKDF2 (RFC 2898) using h as the PRF hash
// (HMAC-h). It matches Python's hashlib.pbkdf2_hmac.
func PBKDF2(password, salt []byte, iter, keyLen int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var dk []byte
	var block [4]byte
	for blockNum := 1; blockNum <= numBlocks; blockNum++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(block[:], uint32(blockNum))
		prf.Write(block[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}
