package runtime

import (
	"strconv"

	"dash/internal/protocol"
)

// MetricsEncoder 复用底层 []byte 缓冲序列化 agent.metrics 报文，
// 稳态下实现零堆内存分配，且热路径绝不使用 fmt.Sprintf。
type MetricsEncoder struct {
	buf []byte
}

// NewMetricsEncoder 创建指定初始容量的可复用编码器。
func NewMetricsEncoder(initialCap int) *MetricsEncoder {
	if initialCap <= 0 {
		initialCap = 2048
	}
	return &MetricsEncoder{
		buf: make([]byte, 0, initialCap),
	}
}

// Reset 清空缓冲区底层游标。
func (e *MetricsEncoder) Reset() {
	e.buf = e.buf[:0]
}

// Bytes 返回当前编码的切片。
func (e *MetricsEncoder) Bytes() []byte {
	return e.buf
}

// Encode 序列化 MetricsParams 到复用缓冲区。
func (e *MetricsEncoder) Encode(m *protocol.MetricsParams) []byte {
	e.Reset()
	b := e.buf

	b = append(b, `{"ts_ms":`...)
	b = strconv.AppendInt(b, m.TsMs, 10)

	if m.CPUPct != nil {
		b = append(b, `,"cpu_pct":`...)
		b = strconv.AppendFloat(b, *m.CPUPct, 'f', -1, 64)
	}
	if m.MemUsed != nil {
		b = append(b, `,"mem_used":`...)
		b = strconv.AppendInt(b, *m.MemUsed, 10)
	}
	if m.SwapUsed != nil {
		b = append(b, `,"swap_used":`...)
		b = strconv.AppendInt(b, *m.SwapUsed, 10)
	}
	if m.Load != nil {
		b = append(b, `,"load":[`...)
		b = strconv.AppendFloat(b, m.Load[0], 'f', -1, 64)
		b = append(b, ',')
		b = strconv.AppendFloat(b, m.Load[1], 'f', -1, 64)
		b = append(b, ',')
		b = strconv.AppendFloat(b, m.Load[2], 'f', -1, 64)
		b = append(b, ']')
	}
	if m.Net != nil {
		b = append(b, `,"net":{`...)
		firstNet := true
		if m.Net.UpBps != nil {
			b = append(b, `"up_bps":`...)
			b = strconv.AppendInt(b, *m.Net.UpBps, 10)
			firstNet = false
		}
		if m.Net.DownBps != nil {
			if !firstNet {
				b = append(b, ',')
			}
			b = append(b, `"down_bps":`...)
			b = strconv.AppendInt(b, *m.Net.DownBps, 10)
			firstNet = false
		}
		if m.Net.TotalUp != nil {
			if !firstNet {
				b = append(b, ',')
			}
			b = append(b, `"total_up":`...)
			b = strconv.AppendInt(b, *m.Net.TotalUp, 10)
			firstNet = false
		}
		if m.Net.TotalDown != nil {
			if !firstNet {
				b = append(b, ',')
			}
			b = append(b, `"total_down":`...)
			b = strconv.AppendInt(b, *m.Net.TotalDown, 10)
		}
		b = append(b, '}')
	}
	if m.UptimeS != nil {
		b = append(b, `,"uptime_s":`...)
		b = strconv.AppendInt(b, *m.UptimeS, 10)
	}
	if m.Slow != nil {
		b = append(b, `,"slow":{`...)
		firstSlow := true
		if m.Slow.DiskUsed != nil {
			b = append(b, `"disk_used":`...)
			b = strconv.AppendInt(b, *m.Slow.DiskUsed, 10)
			firstSlow = false
		}
		if m.Slow.ProcCount != nil {
			if !firstSlow {
				b = append(b, ',')
			}
			b = append(b, `"proc_count":`...)
			b = strconv.AppendInt(b, int64(*m.Slow.ProcCount), 10)
			firstSlow = false
		}
		if m.Slow.TCPCount != nil {
			if !firstSlow {
				b = append(b, ',')
			}
			b = append(b, `"tcp_count":`...)
			b = strconv.AppendInt(b, int64(*m.Slow.TCPCount), 10)
			firstSlow = false
		}
		if m.Slow.UDPCount != nil {
			if !firstSlow {
				b = append(b, ',')
			}
			b = append(b, `"udp_count":`...)
			b = strconv.AppendInt(b, int64(*m.Slow.UDPCount), 10)
			firstSlow = false
		}
		if len(m.Slow.Disks) > 0 {
			if !firstSlow {
				b = append(b, ',')
			}
			b = append(b, `"disks":[`...)
			for i, d := range m.Slow.Disks {
				if i > 0 {
					b = append(b, ',')
				}
				b = append(b, `{"k":`...)
				b = appendEscapedJSONString(b, d.Key)
				if d.Used != nil {
					b = append(b, `,"used":`...)
					b = strconv.AppendInt(b, *d.Used, 10)
				}
				if d.Total != nil {
					b = append(b, `,"total":`...)
					b = strconv.AppendInt(b, *d.Total, 10)
				}
				b = append(b, '}')
			}
			b = append(b, ']')
			firstSlow = false
		}
		if len(m.Slow.NICs) > 0 {
			if !firstSlow {
				b = append(b, ',')
			}
			b = append(b, `"nics":[`...)
			for i, n := range m.Slow.NICs {
				if i > 0 {
					b = append(b, ',')
				}
				b = append(b, `{"k":`...)
				b = appendEscapedJSONString(b, n.Key)
				if n.TotalUp != nil {
					b = append(b, `,"total_up":`...)
					b = strconv.AppendInt(b, *n.TotalUp, 10)
				}
				if n.TotalDown != nil {
					b = append(b, `,"total_down":`...)
					b = strconv.AppendInt(b, *n.TotalDown, 10)
				}
				b = append(b, '}')
			}
			b = append(b, ']')
		}
		b = append(b, '}')
	}

	b = append(b, '}')
	e.buf = b
	return b
}

func appendEscapedJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b = append(b, `\"`...)
		case '\\':
			b = append(b, `\\`...)
		case '\n':
			b = append(b, `\n`...)
		case '\r':
			b = append(b, `\r`...)
		case '\t':
			b = append(b, `\t`...)
		default:
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return b
}
