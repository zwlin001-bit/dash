/**
 * 数据格式化工具函数
 * 遵循 docs/13-ui-spec.md 与 P1-23 规范：
 * - undefined / null / NaN 一律显示 '--'
 * - 禁止用 || 0 掩盖缺失数据（0% 与无数据在监控上有本质区别）
 */

export interface FmtOptions {
  decimals?: number;
  fallback?: string;
}

/**
 * 统一数值与带单位格式化 helper
 * fmt(v, unit, options)
 *
 * 例：
 * fmt(3.5, '%') => '3.5%'
 * fmt(undefined, '%') => '--'
 * fmt(null) => '--'
 * fmt(0, '%') => '0.0%'
 */
export function fmt(
  v: number | string | null | undefined,
  unit?: string,
  options?: FmtOptions
): string {
  const fallback = options?.fallback ?? '--';

  if (v === null || v === undefined || v === '') {
    return fallback;
  }

  if (typeof v === 'number') {
    if (Number.isNaN(v)) {
      return fallback;
    }
    if (unit === 'bytes' || unit === 'B') {
      return formatBytes(v, fallback);
    }
    if (unit === 'bps' || unit === 'B/s') {
      return formatBps(v, fallback);
    }
    const decimals = options?.decimals ?? (unit === '%' ? 1 : undefined);
    const numStr = decimals !== undefined ? v.toFixed(decimals) : `${v}`;
    return unit ? `${numStr}${unit}` : numStr;
  }

  return unit ? `${v}${unit}` : `${v}`;
}

/**
 * 格式化字节大小 (B, KB, MB, GB, TB)
 * null / undefined / NaN 返回 fallback ('--')
 */
export function formatBytes(bytes?: number | null, fallback = '--'): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) {
    return fallback;
  }
  if (bytes <= 0) {
    return '0 B';
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/**
 * 格式化网络速率 (B/s, KB/s, MB/s, GB/s)
 * null / undefined / NaN 返回 fallback ('--')
 */
export function formatBps(bps?: number | null, fallback = '--'): string {
  if (bps === undefined || bps === null || Number.isNaN(bps)) {
    return fallback;
  }
  if (bps <= 0) {
    return '0 B/s';
  }
  if (bps >= 1024 * 1024 * 1024) {
    return `${(bps / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
  }
  if (bps >= 1024 * 1024) {
    return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
  }
  if (bps >= 1024) {
    return `${(bps / 1024).toFixed(0)} KB/s`;
  }
  return `${Math.round(bps)} B/s`;
}

/**
 * 相对时间格式化（例如：刚刚、5 分钟前、从未上线）
 */
export function formatTimeAgo(ms?: number | null, fallback = '从未上线'): string {
  if (!ms || ms <= 0) {
    return fallback;
  }
  const diff = Date.now() - ms;
  if (diff < 10000) return '刚刚';
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  return `${day} 天前`;
}

/**
 * 绝对日期时间格式化 (YYYY-MM-DD HH:mm:ss)
 */
export function formatDateTime(ms?: number | null, fallback = '--'): string {
  if (!ms || ms <= 0) {
    return fallback;
  }
  const d = new Date(ms);
  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const hour = String(d.getHours()).padStart(2, '0');
  const min = String(d.getMinutes()).padStart(2, '0');
  const sec = String(d.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${min}:${sec}`;
}

/**
 * 运行时长格式化 (X 天 Y 小时 / Y 小时 Z 分钟 / Z 分钟)
 */
export function formatUptime(uptimeS?: number | null, fallback = '--'): string {
  if (!uptimeS || uptimeS <= 0) {
    return fallback;
  }
  const d = Math.floor(uptimeS / 86400);
  const h = Math.floor((uptimeS % 86400) / 3600);
  const m = Math.floor((uptimeS % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分钟`;
  return `${m} 分钟`;
}
