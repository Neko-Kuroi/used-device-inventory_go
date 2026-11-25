package xcrypto

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

// Scrypt derives a key with the scrypt KDF (RFC 7914). It matches
// Python's hashlib.scrypt for the same parameters.
func Scrypt(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if n <= 1 || n&(n-1) != 0 {
		return nil, errors.New("scrypt: N must be a power of two greater than 1")
	}
	if r <= 0 || p <= 0 {
		return nil, errors.New("scrypt: r and p must be positive")
	}
	if uint64(r)*uint64(p) >= 1<<30 {
		return nil, errors.New("scrypt: r*p too large")
	}
	b := PBKDF2(password, salt, 1, p*128*r, sha256.New)
	chunk := make([]byte, 128*r)
	for i := 0; i < p; i++ {
		copy(chunk, b[i*128*r:])
		romix(chunk, n, r)
		copy(b[i*128*r:], chunk)
	}
	return PBKDF2(password, b, 1, keyLen, sha256.New), nil
}

func romix(b []byte, n, r int) {
	v := make([][]byte, n)
	x := make([]byte, len(b))
	copy(x, b)
	for i := 0; i < n; i++ {
		v[i] = make([]byte, len(b))
		copy(v[i], x)
		blockMix(x, r)
	}
	for i := 0; i < n; i++ {
		j := integerify(x, r) % uint64(n)
		for k := range x {
			x[k] ^= v[j][k]
		}
		blockMix(x, r)
	}
	copy(b, x)
}

// integerify reads the first 64 bits of the last 64-byte block of b
// as a little-endian integer.
func integerify(b []byte, r int) uint64 {
	last := b[(2*r-1)*64:]
	return binary.LittleEndian.Uint64(last[:8])
}

// blockMix applies the Salsa20/8 core to each 64-byte block of b
// (which holds 2r blocks) and writes the result back in the
// even-then-odd order the algorithm defines.
func blockMix(b []byte, r int) {
	var x [64]byte
	copy(x[:], b[(2*r-1)*64:])
	y := make([]byte, len(b))
	for i := 0; i < 2*r; i++ {
		for j := 0; j < 64; j++ {
			x[j] ^= b[i*64+j]
		}
		salsa208(&x)
		copy(y[i*64:], x[:])
	}
	for i := 0; i < r; i++ {
		copy(b[i*64:], y[2*i*64:])
	}
	for i := 0; i < r; i++ {
		copy(b[(r+i)*64:], y[(2*i+1)*64:])
	}
}

// salsa208 is the Salsa20 core with 8 rounds (4 double rounds),
// including the final addition of the input.
func salsa208(b *[64]byte) {
	var x, orig [16]uint32
	for i := 0; i < 16; i++ {
		x[i] = binary.LittleEndian.Uint32(b[i*4:])
		orig[i] = x[i]
	}
	for round := 0; round < 4; round++ {
		// Column round.
		x[4] ^= bits.RotateLeft32(x[0]+x[12], 7)
		x[8] ^= bits.RotateLeft32(x[4]+x[0], 9)
		x[12] ^= bits.RotateLeft32(x[8]+x[4], 13)
		x[0] ^= bits.RotateLeft32(x[12]+x[8], 18)

		x[9] ^= bits.RotateLeft32(x[5]+x[1], 7)
		x[13] ^= bits.RotateLeft32(x[9]+x[5], 9)
		x[1] ^= bits.RotateLeft32(x[13]+x[9], 13)
		x[5] ^= bits.RotateLeft32(x[1]+x[13], 18)

		x[14] ^= bits.RotateLeft32(x[10]+x[6], 7)
		x[2] ^= bits.RotateLeft32(x[14]+x[10], 9)
		x[6] ^= bits.RotateLeft32(x[2]+x[14], 13)
		x[10] ^= bits.RotateLeft32(x[6]+x[2], 18)

		x[3] ^= bits.RotateLeft32(x[15]+x[11], 7)
		x[7] ^= bits.RotateLeft32(x[3]+x[15], 9)
		x[11] ^= bits.RotateLeft32(x[7]+x[3], 13)
		x[15] ^= bits.RotateLeft32(x[11]+x[7], 18)

		// Row round.
		x[1] ^= bits.RotateLeft32(x[0]+x[3], 7)
		x[2] ^= bits.RotateLeft32(x[1]+x[0], 9)
		x[3] ^= bits.RotateLeft32(x[2]+x[1], 13)
		x[0] ^= bits.RotateLeft32(x[3]+x[2], 18)

		x[6] ^= bits.RotateLeft32(x[5]+x[4], 7)
		x[7] ^= bits.RotateLeft32(x[6]+x[5], 9)
		x[4] ^= bits.RotateLeft32(x[7]+x[6], 13)
		x[5] ^= bits.RotateLeft32(x[4]+x[7], 18)

		x[11] ^= bits.RotateLeft32(x[10]+x[9], 7)
		x[8] ^= bits.RotateLeft32(x[11]+x[10], 9)
		x[9] ^= bits.RotateLeft32(x[8]+x[11], 13)
		x[10] ^= bits.RotateLeft32(x[9]+x[8], 18)

		x[12] ^= bits.RotateLeft32(x[15]+x[14], 7)
		x[13] ^= bits.RotateLeft32(x[12]+x[15], 9)
		x[14] ^= bits.RotateLeft32(x[13]+x[12], 13)
		x[15] ^= bits.RotateLeft32(x[14]+x[13], 18)
	}
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(b[i*4:], x[i]+orig[i])
	}
}
