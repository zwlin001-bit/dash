package linux

import (
	"os"
	"syscall"
)

// GetProcRoot 获取当前系统 proc 挂载根路径，支持 HOST_PROC 环境变量。
func GetProcRoot() string {
	if p := os.Getenv("HOST_PROC"); p != "" {
		return p
	}
	return "/proc"
}

// procReader 复用缓冲读取 proc 文件，支持 pread 零堆分配。
type procReader struct {
	path string
	fd   int
}

func newProcReader(path string) *procReader {
	return &procReader{
		path: path,
		fd:   -1,
	}
}

func (r *procReader) Read(buf []byte) ([]byte, error) {
	if r.fd < 0 {
		fd, err := syscall.Open(r.path, syscall.O_RDONLY, 0)
		if err != nil {
			return nil, err
		}
		r.fd = fd
	}
	n, err := syscall.Pread(r.fd, buf, 0)
	if err != nil || n == 0 {
		_ = syscall.Close(r.fd)
		r.fd = -1
		// 回退重新打开读取
		fd, openErr := syscall.Open(r.path, syscall.O_RDONLY, 0)
		if openErr != nil {
			return nil, openErr
		}
		defer syscall.Close(fd)
		n, readErr := syscall.Read(fd, buf)
		if readErr != nil && n == 0 {
			return nil, readErr
		}
		return buf[:n], nil
	}
	return buf[:n], nil
}

func (r *procReader) Close() {
	if r.fd >= 0 {
		_ = syscall.Close(r.fd)
		r.fd = -1
	}
}

// parseUint 解析十进制无符号整数，无内存分配。
func parseUint(b []byte) (uint64, bool) {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	if i >= len(b) {
		return 0, false
	}
	var n uint64
	hasDigit := false
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		hasDigit = true
		n = n*10 + uint64(b[i]-'0')
		i++
	}
	return n, hasDigit
}

// parseInt 解析十进制有符号整数，无内存分配。
func parseInt(b []byte) (int64, bool) {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	if i >= len(b) {
		return 0, false
	}
	neg := false
	if b[i] == '-' {
		neg = true
		i++
	} else if b[i] == '+' {
		i++
	}
	var n int64
	hasDigit := false
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		hasDigit = true
		n = n*10 + int64(b[i]-'0')
		i++
	}
	if !hasDigit {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

// parseFloat 解析浮点数，无内存分配。
func parseFloat(b []byte) (float64, bool) {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	if i >= len(b) {
		return 0, false
	}
	neg := false
	if b[i] == '-' {
		neg = true
		i++
	} else if b[i] == '+' {
		i++
	}
	var intPart uint64
	hasDigit := false
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		hasDigit = true
		intPart = intPart*10 + uint64(b[i]-'0')
		i++
	}
	val := float64(intPart)
	if i < len(b) && b[i] == '.' {
		i++
		var fracPart uint64
		divisor := 1.0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			hasDigit = true
			fracPart = fracPart*10 + uint64(b[i]-'0')
			divisor *= 10.0
			i++
		}
		val += float64(fracPart) / divisor
	}
	if !hasDigit {
		return 0, false
	}
	if neg {
		val = -val
	}
	return val, true
}

// nextField 提取下一个以空白分隔的字段，返回 (field, remaining)。
func nextField(b []byte) ([]byte, []byte) {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	if i >= len(b) {
		return nil, nil
	}
	start := i
	for i < len(b) && b[i] != ' ' && b[i] != '\t' && b[i] != '\r' && b[i] != '\n' {
		i++
	}
	return b[start:i], b[i:]
}

// findLine 查找以 prefix 开头的行，并返回 prefix 之后的内容。
func findLine(buf []byte, prefix []byte) []byte {
	for len(buf) > 0 {
		lineEnd := -1
		for i, c := range buf {
			if c == '\n' {
				lineEnd = i
				break
			}
		}
		var line []byte
		if lineEnd >= 0 {
			line = buf[:lineEnd]
			buf = buf[lineEnd+1:]
		} else {
			line = buf
			buf = nil
		}
		if len(line) >= len(prefix) {
			match := true
			for i := 0; i < len(prefix); i++ {
				if line[i] != prefix[i] {
					match = false
					break
				}
			}
			if match {
				return line[len(prefix):]
			}
		}
	}
	return nil
}
