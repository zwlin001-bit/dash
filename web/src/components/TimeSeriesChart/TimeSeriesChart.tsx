import React, { useEffect, useRef, useState, useMemo } from 'react';
import * as echarts from 'echarts';
import { MetricsQueryResponse, TimeSeriesSpan } from '../../api/types';
import styles from './TimeSeriesChart.module.css';

export interface MetricSeriesConfig {
  key: string;
  name?: string;
  unit?: string;
  color?: string; // 可选自定义颜色变量名，默认按顺序取系列色
}

export interface TimeSeriesChartProps {
  data?: MetricsQueryResponse | any;
  span?: TimeSeriesSpan;
  onSpanChange?: (span: TimeSeriesSpan) => void;
  title?: string;
  metrics?: (string | MetricSeriesConfig)[]; // 要展示的指标字段列表（支持多指标叠加）
  height?: number | string;
  loading?: boolean;
  threshold?: {
    value: number;
    label?: string;
    color?: string;
  };
  sourceBadge?: string;
}

// 默认系列色变量名称顺序（docs/13-ui-spec.md §7）
const DEFAULT_COLOR_VARS = [
  '--color-blue',
  '--color-teal',
  '--color-yellow',
  '--color-purple',
  '--color-cyan',
  '--color-red',
];

// 常见指标的默认中文名称与单位映射
const METRIC_META: Record<string, { name: string; unit: string }> = {
  cpu_pct: { name: 'CPU 使用率', unit: '%' },
  mem_used: { name: '内存已用', unit: 'bytes' },
  swap_used: { name: 'Swap 已用', unit: 'bytes' },
  net_up_bps: { name: '上传速率', unit: 'bytes/s' },
  net_down_bps: { name: '下载速率', unit: 'bytes/s' },
  net_total_up: { name: '总上传量', unit: 'bytes' },
  net_total_down: { name: '总下载量', unit: 'bytes' },
  traffic_up: { name: '上传流量', unit: 'bytes' },
  traffic_down: { name: '下载流量', unit: 'bytes' },
  load1: { name: 'Load 1m', unit: '' },
  load5: { name: 'Load 5m', unit: '' },
  load15: { name: 'Load 15m', unit: '' },
  disk_used: { name: '磁盘已用', unit: 'bytes' },
  proc_count: { name: '进程数', unit: '' },
  tcp_count: { name: 'TCP 连接数', unit: '' },
  udp_count: { name: 'UDP 连接数', unit: '' },
  uptime_s: { name: '运行时间', unit: 's' },
  traffic_month_up: { name: '月度 CDT 出网流量', unit: 'bytes' },
};

function formatValueWithUnit(val: number | null | undefined, unit: string): string {
  if (val === null || val === undefined || isNaN(val)) {
    return '--';
  }
  if (unit === '%') {
    return `${val.toFixed(1)}%`;
  }
  if (unit === 'bytes/s' || unit === 'B/s') {
    if (val >= 1024 * 1024 * 1024) return `${(val / (1024 * 1024 * 1024)).toFixed(2)} GB/s`;
    if (val >= 1024 * 1024) return `${(val / (1024 * 1024)).toFixed(2)} MB/s`;
    if (val >= 1024) return `${(val / 1024).toFixed(1)} KB/s`;
    return `${Math.round(val)} B/s`;
  }
  if (unit === 'bytes' || unit === 'B') {
    if (val >= 1024 * 1024 * 1024 * 1024) return `${(val / (1024 * 1024 * 1024 * 1024)).toFixed(2)} TB`;
    if (val >= 1024 * 1024 * 1024) return `${(val / (1024 * 1024 * 1024)).toFixed(2)} GB`;
    if (val >= 1024 * 1024) return `${(val / (1024 * 1024)).toFixed(1)} MB`;
    if (val >= 1024) return `${(val / 1024).toFixed(0)} KB`;
    return `${Math.round(val)} B`;
  }
  if (unit === 's') {
    const d = Math.floor(val / 86400);
    const h = Math.floor((val % 86400) / 3600);
    return d > 0 ? `${d}d ${h}h` : `${h}h`;
  }
  return typeof val === 'number' && !Number.isInteger(val) ? val.toFixed(2) : `${val}`;
}

export const TimeSeriesChart: React.FC<TimeSeriesChartProps> = ({
  data,
  span = '6h',
  onSpanChange,
  title,
  metrics,
  height = 320,
  loading = false,
  threshold,
  sourceBadge,
}) => {
  const chartRef = useRef<HTMLDivElement>(null);
  const chartInstanceRef = useRef<echarts.ECharts | null>(null);
  const [themeVersion, setThemeVersion] = useState(0);

  // 解析并标准化需要渲染的系列配置
  const seriesConfigs: MetricSeriesConfig[] = useMemo(() => {
    if (!data?.series) return [];
    const availableKeys = Object.keys(data.series);
    if (!metrics || metrics.length === 0) {
      return availableKeys.map((key) => ({
        key,
        name: METRIC_META[key]?.name || key,
        unit: METRIC_META[key]?.unit || '',
      }));
    }
    return metrics.map((m) => {
      if (typeof m === 'string') {
        return {
          key: m,
          name: METRIC_META[m]?.name || m,
          unit: METRIC_META[m]?.unit || '',
        };
      }
      return {
        ...m,
        name: m.name || METRIC_META[m.key]?.name || m.key,
        unit: m.unit !== undefined ? m.unit : METRIC_META[m.key]?.unit || '',
      };
    });
  }, [data?.series, metrics]);

  // 主题切换监听：观察 html 标签属性或系统变化，触发重新取色而不重建实例（13-ui-spec.md §7）
  useEffect(() => {
    const observer = new MutationObserver(() => {
      setThemeVersion((v) => v + 1);
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });

    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    const handleMediaChange = () => setThemeVersion((v) => v + 1);
    mediaQuery.addEventListener('change', handleMediaChange);

    return () => {
      observer.disconnect();
      mediaQuery.removeEventListener('change', handleMediaChange);
    };
  }, []);

  // 从 CSS 变量实时读取颜色（禁止在 JS 中写死十六进制）
  const themeColors = useMemo(() => {
    if (typeof window === 'undefined' || typeof document === 'undefined') {
      return {
        seriesColors: ['#4da6ff', '#00d4aa', '#ffb870', '#b392f0', '#39d2c0', '#f85149'],
        borderLight: '#2d3b4e',
        borderDim: '#1a2330',
        textMain: '#d3dae3',
        textDim: '#8999af',
        textMute: '#55657a',
        bgCard: '#161d27',
        bgCardSub: '#1c2431',
        colorErr: '#f85149',
      };
    }
    const stylesObj = getComputedStyle(document.documentElement);
    return {
      seriesColors: DEFAULT_COLOR_VARS.map((varName) => stylesObj.getPropertyValue(varName).trim() || '#4da6ff'),
      borderLight: stylesObj.getPropertyValue('--border-light').trim() || '#2d3b4e',
      borderDim: stylesObj.getPropertyValue('--border-dim').trim() || '#1a2330',
      textMain: stylesObj.getPropertyValue('--text-main').trim() || '#d3dae3',
      textDim: stylesObj.getPropertyValue('--text-dim').trim() || '#8999af',
      textMute: stylesObj.getPropertyValue('--text-mute').trim() || '#55657a',
      bgCard: stylesObj.getPropertyValue('--bg-card').trim() || '#161d27',
      bgCardSub: stylesObj.getPropertyValue('--bg-card-sub').trim() || '#1c2431',
      colorErr: stylesObj.getPropertyValue('--err').trim() || '#f85149',
    };
  }, [themeVersion]);

  // 初始化 ECharts 实例并绑定 ResizeObserver
  useEffect(() => {
    if (!chartRef.current) return;
    const chart = echarts.init(chartRef.current);
    chartInstanceRef.current = chart;

    const ro = new ResizeObserver(() => {
      chart.resize();
    });
    ro.observe(chartRef.current);

    return () => {
      ro.disconnect();
      chart.dispose();
      chartInstanceRef.current = null;
    };
  }, []);

  // 更新图表选项（跨度切换只换数据，切主题只重新取色并 setOption）
  useEffect(() => {
    const chart = chartInstanceRef.current;
    if (!chart) return;

    if (!data || !data.ts_ms || data.ts_ms.length === 0 || seriesConfigs.length === 0) {
      chart.clear();
      return;
    }

    const { ts_ms, series: dataSeries } = data;

    // 构建 ECharts 系列
    const echartsSeries: echarts.SeriesOption[] = seriesConfigs.map((cfg, idx) => {
      const color = themeColors.seriesColors[idx % themeColors.seriesColors.length];
      const seriesValues = (dataSeries && dataSeries[cfg.key]) || [];

      // 时间戳 (ms) 与值一一对应配对
      const points: [number, number | null][] = ts_ms.map((t: number, i: number) => [t, seriesValues[i] ?? null]);

      const seriesOpt: echarts.SeriesOption = {
        name: cfg.name,
        type: 'line',
        showSymbol: false,
        smooth: false,
        // ★ 核心约束：null 值断线，不连成直线（docs/13-ui-spec.md §7）
        connectNulls: false,
        lineStyle: {
          width: 2,
          color,
        },
        itemStyle: {
          color,
        },
        areaStyle:
          seriesConfigs.length <= 2
            ? {
                color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                  { offset: 0, color: `${color}33` },
                  { offset: 1, color: `${color}00` },
                ]),
              }
            : undefined,
        data: points,
      };

      if (idx === 0 && threshold) {
        const threshColor = threshold.color || themeColors.colorErr;
        (seriesOpt as any).markLine = {
          symbol: 'none',
          silent: false,
          data: [
            {
              yAxis: threshold.value,
              lineStyle: {
                color: threshColor,
                type: 'dashed',
                width: 1.5,
              },
              label: {
                show: true,
                position: 'insideEndTop',
                formatter: threshold.label || `阈值: ${formatValueWithUnit(threshold.value, cfg.unit || '')}`,
                color: threshColor,
                fontSize: 11,
                fontFamily: 'var(--font-mono)',
              },
            },
          ],
        };
      }

      return seriesOpt;
    });

    const option: echarts.EChartsOption = {
      animation: false,
      color: themeColors.seriesColors,
      grid: {
        top: 28,
        right: 18,
        bottom: 30,
        left: 54,
        containLabel: true,
      },
      tooltip: {
        trigger: 'axis',
        backgroundColor: themeColors.bgCard,
        borderColor: themeColors.borderLight,
        textStyle: {
          color: themeColors.textMain,
          fontSize: 12,
          fontFamily: 'var(--font-sans)',
        },
        axisPointer: {
          type: 'line',
          lineStyle: {
            color: themeColors.borderLight,
            type: 'dashed',
          },
        },
        formatter: (params: any) => {
          if (!Array.isArray(params) || params.length === 0) return '';
          const firstPoint = params[0];
          const timeMs = firstPoint.value[0];
          const timeStr = new Date(timeMs).toLocaleString('zh-CN', {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hour12: false,
          });

          let html = `<div style="font-family: var(--font-mono); font-size: 11px; color: ${themeColors.textDim}; margin-bottom: 6px;">${timeStr}</div>`;
          for (const p of params) {
            const val = p.value[1];
            const cfg = seriesConfigs.find((c) => c.name === p.seriesName);
            const unit = cfg?.unit || '';
            const formatted = formatValueWithUnit(val, unit);
            html += `
              <div style="display: flex; align-items: center; justify-content: space-between; gap: 16px; margin: 3px 0; font-size: 12px;">
                <span style="display: flex; align-items: center; gap: 6px;">
                  <span style="display:inline-block; width:8px; height:8px; border-radius:50%; background:${p.color};"></span>
                  <span style="color:${themeColors.textMain}">${p.seriesName}</span>
                </span>
                <span style="font-family: var(--font-mono); font-weight: 600; color: ${themeColors.textMain}">${formatted}</span>
              </div>
            `;
          }
          return html;
        },
      },
      legend: {
        show: seriesConfigs.length > 1,
        top: 0,
        right: 10,
        textStyle: {
          color: themeColors.textDim,
          fontSize: 11,
          fontFamily: 'var(--font-sans)',
        },
        icon: 'roundRect',
        itemWidth: 12,
        itemHeight: 6,
      },
      xAxis: {
        type: 'time',
        axisLine: {
          lineStyle: { color: themeColors.borderDim },
        },
        splitLine: {
          show: true,
          lineStyle: { color: themeColors.borderDim, type: 'dotted' },
        },
        axisLabel: {
          color: themeColors.textDim,
          fontSize: 10,
          fontFamily: 'var(--font-mono)',
          formatter: (value: number) => {
            const d = new Date(value);
            if (span === '6h') {
              return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
            }
            if (span === '3d') {
              return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:00`;
            }
            return `${d.getMonth() + 1}/${d.getDate()}`;
          },
        },
      },
      yAxis: {
        type: 'value',
        axisLine: { show: false },
        splitLine: {
          show: true,
          lineStyle: { color: themeColors.borderLight, type: 'dashed' },
        },
        axisLabel: {
          color: themeColors.textDim,
          fontSize: 10,
          fontFamily: 'var(--font-mono)',
          formatter: (value: number) => {
            const firstUnit = seriesConfigs[0]?.unit || '';
            return formatValueWithUnit(value, firstUnit);
          },
        },
      },
      series: echartsSeries,
    };

    chart.setOption(option, { notMerge: true });
  }, [data, seriesConfigs, themeColors, span, threshold]);

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <div className={styles.titleArea}>
          {title && <span className={styles.title}>{title}</span>}
          {sourceBadge && <span className={styles.sourceBadge}>{sourceBadge}</span>}
          {!sourceBadge && data?.source && (
            <span className={styles.sourceBadge}>
              表: {data.source}
              {data.ts_ms ? ` (${data.ts_ms.length} 采样点)` : ''}
            </span>
          )}
        </div>
        <div className={styles.controls}>
          <div className={styles.spanButtons}>
            {(['6h', '3d', '60d', '1y'] as TimeSeriesSpan[]).map((s) => (
              <button
                key={s}
                type="button"
                className={`${styles.spanBtn} ${span === s ? styles.active : ''}`}
                onClick={() => onSpanChange && onSpanChange(s)}
              >
                {s}
              </button>
            ))}
          </div>
        </div>
      </div>
      <div className={styles.chartWrap} style={{ height }}>
        {loading && <div className={styles.loadingOverlay}>数据加载中...</div>}
        <div ref={chartRef} className={styles.chartDom} />
      </div>
    </div>
  );
};
