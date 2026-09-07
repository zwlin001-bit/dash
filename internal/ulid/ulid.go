package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"sync"
	"time"
)

// Crockford's Base32 encoding alphabet (excludes I, L, O, U to avoid confusion).
const encoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	entropyMu sync.Mutex
	entropy   io.Reader = rand.Reader
)

// New generates a new 26-character ULID string using the current time and crypto/rand entropy.
func New() string {
	return NewWithTime(time.Now())
}

// NewWithTime generates a new 26-character ULID string using the provided time.
func NewWithTime(t time.Time) string {
	ms := uint64(t.UnixMilli())

	var entropyBytes [10]byte
	entropyMu.Lock()
	_, _ = io.ReadFull(entropy, entropyBytes[:])
	entropyMu.Unlock()

	return encode(ms, entropyBytes)
}

// IsValid reports whether s is a valid 26-character ULID string.
func IsValid(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') ||
			(c >= 'A' && c <= 'H') ||
			(c >= 'J' && c <= 'K') ||
			(c >= 'M' && c <= 'N') ||
			(c >= 'P' && c <= 'T') ||
			(c >= 'V' && c <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func encode(ms uint64, randBytes [10]byte) string {
	var raw [16]byte
	// 48-bit timestamp (big endian into first 6 bytes)
	var timeBuf [8]byte
	binary.BigEndian.PutUint64(timeBuf[:], ms)
	copy(raw[0:6], timeBuf[2:8])
	// 80-bit randomness
	copy(raw[6:16], randBytes[:])

	// Encode 128 bits into 26 Base32 characters
	var dst [26]byte

	// 10 chars for 48 bits of timestamp
	dst[0] = encoding[(raw[0]&224)>>5]
	dst[1] = encoding[raw[0]&31]
	dst[2] = encoding[(raw[1]&248)>>3]
	dst[3] = encoding[((raw[1]&7)<<2)|((raw[2]&192)>>6)]
	dst[4] = encoding[(raw[2]&62)>>1]
	dst[5] = encoding[((raw[2]&1)<<4)|((raw[3]&240)>>4)]
	dst[6] = encoding[((raw[3]&15)<<1)|((raw[4]&128)>>7)]
	dst[7] = encoding[(raw[4]&124)>>2]
	dst[8] = encoding[((raw[4]&3)<<3)|((raw[5]&224)>>5)]
	dst[9] = encoding[raw[5]&31]

	// 16 chars for 80 bits of randomness
	dst[10] = encoding[(raw[6]&248)>>3]
	dst[11] = encoding[((raw[6]&7)<<2)|((raw[7]&192)>>6)]
	dst[12] = encoding[(raw[7]&62)>>1]
	dst[13] = encoding[((raw[7]&1)<<4)|((raw[8]&240)>>4)]
	dst[14] = encoding[((raw[8]&15)<<1)|((raw[9]&128)>>7)]
	dst[15] = encoding[(raw[9]&124)>>2]
	dst[16] = encoding[((raw[9]&3)<<3)|((raw[10]&224)>>5)]
	dst[17] = encoding[raw[10]&31]
	dst[18] = encoding[(raw[11]&248)>>3]
	dst[19] = encoding[((raw[11]&7)<<2)|((raw[12]&192)>>6)]
	dst[20] = encoding[(raw[12]&62)>>1]
	dst[21] = encoding[((raw[12]&1)<<4)|((raw[13]&240)>>4)]
	dst[22] = encoding[((raw[13]&15)<<1)|((raw[14]&128)>>7)]
	dst[23] = encoding[(raw[14]&124)>>2]
	dst[24] = encoding[((raw[14]&3)<<3)|((raw[15]&224)>>5)]
	dst[25] = encoding[raw[15]&31]

	return string(dst[:])
}
