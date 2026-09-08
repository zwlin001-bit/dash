import React, { useState, useId } from 'react';
import styles from './Sparkline.module.css';

export interface SparklineProps {
  points?: (number | null)[];
  timestamps?: number[];
  threshold?: number;
  unit?: string;
  metricLabel?: string;
  emptyText?: string;
}

function formatValue(val: number | null | undefined, unit: string): string {
  if (val === null || val === undefined || isNaN(val)) return '--';
  if (unit === '%') return `${val.toFixed(1)}%`;
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
  if (unit === 'GB') {
    return `${val.toFixed(2)} GB`;
  }
  return typeof val === 'number' && !Number.isInteger(val) ? val.toFixed(2) : `${val}`;
}

export const Sparkline: React.FC<SparklineProps> = ({
  points = [],
  timestamps = [],
  threshold,
  unit = '',
  metricLabel = '采样值',
  emptyText = '暂无云监控采样数据（停机或未产生采样）',
}) => {
  const gradId = useId().replace(/:/g, '-');
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const [mousePos, setMousePos] = useState<{ x: number; y: number } | null>(null);

  // 提取有效数据点
  const validIndices: number[] = [];
  const validValues: number[] = [];
  points.forEach((p, idx) => {
    if (p !== null && p !== undefined && !isNaN(p)) {
      validIndices.push(idx);
      validValues.push(p);
    }
  });

  if (points.length === 0 || validValues.length === 0) {
    return (
      <div className={styles.sparklineWrapper}>
        <div className={styles.chartEmpty}>{emptyText}</div>
      </div>
    );
  }

  // 坐标映射: viewBox 0 0 400 80
  const width = 400;
  const height = 80;
  const padX = 8;
  const padTop = 12;
  const padBottom = 8;
  const plotWidth = width - padX * 2;
  const plotHeight = height - padTop - padBottom;

  const minVal = 0;
  let maxVal = Math.max(...validValues, threshold ?? 0);
  if (maxVal <= minVal) maxVal = 100;
  maxVal = maxVal * 1.1; // 顶部留 10% 留白

  const getX = (idx: number) => padX + (idx / Math.max(points.length - 1, 1)) * plotWidth;
  const getY = (val: number) => height - padBottom - ((val - minVal) / (maxVal - minVal)) * plotHeight;

  // 生成折线与填充路径（支持跳过 null 断线）
  let linePath = '';
  let areaPath = '';
  let segmentStart: number | null = null;

  for (let i = 0; i < points.length; i++) {
    const val = points[i];
    if (val !== null && val !== undefined && !isNaN(val)) {
      const x = getX(i);
      const y = getY(val);
      if (segmentStart === null) {
        segmentStart = i;
        linePath += `M ${x.toFixed(1)} ${y.toFixed(1)}`;
        areaPath += `M ${x.toFixed(1)} ${height - padBottom} L ${x.toFixed(1)} ${y.toFixed(1)}`;
      } else {
        linePath += ` L ${x.toFixed(1)} ${y.toFixed(1)}`;
        areaPath += ` L ${x.toFixed(1)} ${y.toFixed(1)}`;
      }
    } else {
      if (segmentStart !== null) {
        const lastValidX = getX(i - 1);
        areaPath += ` L ${lastValidX.toFixed(1)} ${height - padBottom} Z `;
        segmentStart = null;
      }
    }
  }
  if (segmentStart !== null) {
    const lastValidIdx = validIndices[validIndices.length - 1];
    const lastValidX = getX(lastValidIdx);
    areaPath += ` L ${lastValidX.toFixed(1)} ${height - padBottom} Z`;
  }

  // 当前最新点（最后一个有效采样点）
  const lastIdx = validIndices[validIndices.length - 1];
  const lastVal = points[lastIdx]!;
  const lastPoint = { x: getX(lastIdx), y: getY(lastVal) };

  // 阈值基准线
  const baselineY = threshold !== undefined && threshold > 0 ? getY(threshold) : null;

  // 交互悬停处理
  const handleMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const relX = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    const targetIdx = Math.round(relX * Math.max(points.length - 1, 1));
    setHoverIdx(targetIdx);
    setMousePos({ x: e.clientX - rect.left, y: e.clientY - rect.top });
  };

  const handleMouseLeave = () => {
    setHoverIdx(null);
    setMousePos(null);
  };

  const activeIdx = hoverIdx !== null ? hoverIdx : null;
  const activeVal = activeIdx !== null ? points[activeIdx] : null;
  const activeTs = activeIdx !== null && timestamps[activeIdx] ? timestamps[activeIdx] : null;

  return (
    <div className={styles.sparklineWrapper}>
      <svg
        className={styles.sparklineSvg}
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        onMouseMove={handleMouseMove}
        onMouseLeave={handleMouseLeave}
      >
        <defs>
          <linearGradient id={`spark-area-grad-${gradId}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.32" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0.0" />
          </linearGradient>
        </defs>

        {/* 1. 面积渐变填充 */}
        {areaPath && (
          <path className={styles.chartArea} d={areaPath} fill={`url(#spark-area-grad-${gradId})`} />
        )}

        {/* 2. 虚线基准线 (流量/指标阈值线) */}
        {baselineY !== null && baselineY >= padTop && baselineY <= height - padBottom && (
          <g>
            <line
              className={styles.chartBaseline}
              x1={padX}
              y1={baselineY}
              x2={width - padX}
              y2={baselineY}
              strokeDasharray="3 5"
            />
            <text
              x={width - padX - 4}
              y={Math.max(baselineY - 4, padTop + 2)}
              textAnchor="end"
              className={styles.chartBaselineLabel}
            >
              {`阈值: ${formatValue(threshold, unit)}`}
            </text>
          </g>
        )}

        {/* 1. 圆角折线 */}
        {linePath && (
          <path
            className={styles.chartLine}
            d={linePath}
            fill="none"
            stroke="var(--accent)"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        )}

        {/* 3. 当前点脉冲动画 */}
        <circle className={styles.chartPoint} cx={lastPoint.x} cy={lastPoint.y} r="3.5" />

        {/* 4. hover 悬停定位十字线与焦点 */}
        {activeIdx !== null && (
          <g>
            <line
              className={styles.chartFocus}
              x1={getX(activeIdx)}
              y1={padTop}
              x2={getX(activeIdx)}
              y2={height - padBottom}
              strokeDasharray="2 2"
            />
            {activeVal !== null && activeVal !== undefined && (
              <circle
                className={styles.chartFocusDot}
                cx={getX(activeIdx)}
                cy={getY(activeVal)}
                r="4"
              />
            )}
          </g>
        )}
      </svg>

      {/* 4. hover 悬浮 tooltip (60px + 1fr 布局) */}
      {activeIdx !== null && mousePos && (
        <div
          className={styles.chartTooltip}
          style={{
            left: `${Math.max(70, Math.min(width - 70, mousePos.x))}px`,
          }}
        >
          <span className={styles.tooltipLabel}>时间</span>
          <span className={styles.tooltipValue}>
            {activeTs ? new Date(activeTs).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : `点 #${activeIdx + 1}`}
          </span>
          <span className={styles.tooltipLabel}>{metricLabel}</span>
          <span className={styles.tooltipValue}>{formatValue(activeVal, unit)}</span>
          {threshold !== undefined && (
            <>
              <span className={styles.tooltipLabel}>阈值</span>
              <span className={styles.tooltipValue}>{formatValue(threshold, unit)}</span>
            </>
          )}
        </div>
      )}
    </div>
  );
};
