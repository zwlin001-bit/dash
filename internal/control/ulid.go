package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const crockfordBase32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu       sync.Mutex
	lastULIDTime int64
	lastEntropy  [10]byte
)

// NewULID 生成符合 Crockford Base32 编码规范的 26 位 ULID。
// 保证同毫秒内连续生成的单调递增性。
func NewULID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	now := time.Now().UnixMilli()
	var entropy [10]byte

	if now == lastULIDTime {
		entropy = lastEntropy
		for i := len(entropy) - 1; i >= 0; i-- {
			entropy[i]++
			if entropy[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(entropy[:]); err != nil {
			entropy[0] = byte(now)
		}
		lastULIDTime = now
	}
	lastEntropy = entropy

	return encodeULID(now, entropy)
}

func encodeULID(timeMs int64, entropy [10]byte) string {
	var b [16]byte
	b[0] = byte(timeMs >> 40)
	b[1] = byte(timeMs >> 32)
	b[2] = byte(timeMs >> 24)
	b[3] = byte(timeMs >> 16)
	b[4] = byte(timeMs >> 8)
	b[5] = byte(timeMs)
	copy(b[6:], entropy[:])

	var dst [26]byte
	// 48-bit time -> 10 Crockford Base32 字符
	dst[0] = crockfordBase32[(b[0]>>5)&31]
	dst[1] = crockfordBase32[b[0]&31]
	dst[2] = crockfordBase32[(b[1]>>3)&31]
	dst[3] = crockfordBase32[((b[1]&7)<<2)|((b[2]>>6)&3)]
	dst[4] = crockfordBase32[(b[2]>>1)&31]
	dst[5] = crockfordBase32[((b[2]&1)<<4)|((b[3]>>4)&15)]
	dst[6] = crockfordBase32[((b[3]&15)<<1)|((b[4]>>7)&1)]
	dst[7] = crockfordBase32[(b[4]>>2)&31]
	dst[8] = crockfordBase32[((b[4]&3)<<3)|((b[5]>>5)&7)]
	dst[9] = crockfordBase32[b[5]&31]

	// 80-bit entropy -> 16 Crockford Base32 字符
	dst[10] = crockfordBase32[(b[6]>>3)&31]
	dst[11] = crockfordBase32[((b[6]&7)<<2)|((b[7]>>6)&3)]
	dst[12] = crockfordBase32[(b[7]>>1)&31]
	dst[13] = crockfordBase32[((b[7]&1)<<4)|((b[8]>>4)&15)]
	dst[14] = crockfordBase32[((b[8]&15)<<1)|((b[9]>>7)&1)]
	dst[15] = crockfordBase32[(b[9]>>2)&31]
	dst[16] = crockfordBase32[((b[9]&3)<<3)|((b[10]>>5)&7)]
	dst[17] = crockfordBase32[b[10]&31]
	dst[18] = crockfordBase32[(b[11]>>3)&31]
	dst[19] = crockfordBase32[((b[11]&7)<<2)|((b[12]>>6)&3)]
	dst[20] = crockfordBase32[(b[12]>>1)&31]
	dst[21] = crockfordBase32[((b[12]&1)<<4)|((b[13]>>4)&15)]
	dst[22] = crockfordBase32[((b[13]&15)<<1)|((b[14]>>7)&1)]
	dst[23] = crockfordBase32[(b[14]>>2)&31]
	dst[24] = crockfordBase32[((b[14]&3)<<3)|((b[15]>>5)&7)]
	dst[25] = crockfordBase32[b[15]&31]

	return string(dst[:])
}

// GenerateRandomToken 生成 32 字节高熵随机十六进制字符串（64 字符）。
func GenerateRandomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("crypto/rand read failed: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// HashToken 计算 Token 的 SHA-256 十六进制摘要。
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// extractClientIP 从 HTTP 请求中提取客户端公网/真实 IP 地址。
// 支持 X-Forwarded-For、X-Real-IP 及 RemoteAddr。
func extractClientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
