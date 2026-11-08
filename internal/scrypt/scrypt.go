// Package scrypt implements scrypt (RFC 7914) with the standard library only, so dashboard passwords
// hashed by the earlier Node server (crypto.scryptSync defaults: N=16384, r=8, p=1) keep working.
package scrypt

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

func salsa208(b *[16]uint32) {
	x := *b
	for i := 0; i < 8; i += 2 {
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
	for i := range b {
		b[i] += x[i]
	}
}

func blockMix(b []uint32, y []uint32, r int) {
	var x [16]uint32
	copy(x[:], b[(2*r-1)*16:])
	for i := 0; i < 2*r; i++ {
		for j := 0; j < 16; j++ {
			x[j] ^= b[i*16+j]
		}
		salsa208(&x)
		// even blocks go to the first half, odd to the second
		off := (i/2)*16 + (i%2)*r*16
		copy(y[off:off+16], x[:])
	}
	copy(b, y)
}

func roMix(block []byte, r, n int) {
	words := 32 * r
	x := make([]uint32, words)
	y := make([]uint32, words)
	v := make([]uint32, words*n)
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(block[i*4:])
	}
	for i := 0; i < n; i++ {
		copy(v[i*words:], x)
		blockMix(x, y, r)
	}
	for i := 0; i < n; i++ {
		j := int(x[(2*r-1)*16] & uint32(n-1))
		for k := range x {
			x[k] ^= v[j*words+k]
		}
		blockMix(x, y, r)
	}
	for i := range x {
		binary.LittleEndian.PutUint32(block[i*4:], x[i])
	}
}

// Key derives a key of keyLen bytes. N must be a power of two greater than 1.
func Key(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if n <= 1 || n&(n-1) != 0 || r <= 0 || p <= 0 || r*p >= 1<<30 || n > 1<<20 {
		return nil, errors.New("scrypt: bad parameters")
	}
	b, err := pbkdf2.Key(sha256.New, string(password), salt, 1, p*128*r)
	if err != nil {
		return nil, err
	}
	for i := 0; i < p; i++ {
		roMix(b[i*128*r:(i+1)*128*r], r, n)
	}
	return pbkdf2.Key(sha256.New, string(password), b, 1, keyLen)
}
