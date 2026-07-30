import { useMemo } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { useECharts } from '@/hooks/use-echarts'
import { useTheme } from '@/lib/theme'
import { CATEGORICAL_COLOR, CHART_INK } from '@/lib/chart-theme'
import { type Interval } from '@/lib/time-range'
import { formatMetricValue, seriesLabel } from '@/features/infrastructure/metric-queries'
import { Skeleton } from '@/components/ui/skeleton'
import type { MetricSeries } from '@/api/generated'

/**
 * Multi-series line chart for a PromQL matrix result, following the same
 * dataviz method as the metrics explorer: one categorical hue per series
 * (2px round line), hairline gridlines, recessive axis ink, a crosshair
 * axis tooltip sorted descending, and a scrollable legend (topk queries can
 * return ~10 series). Legend names come from the series label set.
 */
export function MetricChart({ series, interval }: { series: MetricSeries[]; interval: Interval }) {
  const { theme } = useTheme()

  const option = useMemo<EChartsCoreOption>(() => {
    const ink = CHART_INK[theme]
    const palette = CATEGORICAL_COLOR[theme]

    // ECharts keys series/legend by name, so names must be unique — suffix
    // any collision (e.g. two unlabelled aggregates) with an index.
    const seen = new Map<string, number>()
    const uniqueName = (base: string): string => {
      const n = seen.get(base) ?? 0
      seen.set(base, n + 1)
      return n === 0 ? base : `${base} (${n + 1})`
    }

    const lines = series.map((s, index) => {
      const color = palette[index % palette.length]
      return {
        name: uniqueName(seriesLabel(s.labels)),
        type: 'line' as const,
        data: s.points.map((p) => [new Date(p.ts).getTime(), p.value]),
        showSymbol: false,
        lineStyle: { color, width: 2, join: 'round' as const, cap: 'round' as const },
        itemStyle: { color },
        emphasis: { disabled: true },
      }
    })

    return {
      backgroundColor: 'transparent',
      animation: false,
      grid: { left: 8, right: 12, top: 40, bottom: 4, containLabel: true },
      legend: {
        type: 'scroll',
        top: 0,
        left: 0,
        right: 12,
        icon: 'roundRect',
        itemWidth: 10,
        itemHeight: 3,
        itemGap: 14,
        textStyle: { color: ink.label, fontSize: 11 },
        pageTextStyle: { color: ink.label },
        pageIconColor: ink.label,
        pageIconInactiveColor: ink.axisLine,
        inactiveColor: ink.axisLine,
      },
      xAxis: {
        type: 'time',
        min: new Date(interval.from).getTime(),
        max: new Date(interval.to).getTime(),
        axisLine: { lineStyle: { color: ink.axisLine, width: 1 } },
        axisTick: { show: false },
        axisLabel: { color: ink.label, fontSize: 11, hideOverlap: true },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: ink.label, fontSize: 11, formatter: (v: number) => formatMetricValue(v) },
        splitLine: { lineStyle: { color: ink.grid, width: 1, type: 'solid' } },
        splitNumber: 3,
      },
      tooltip: {
        trigger: 'axis',
        order: 'valueDesc',
        axisPointer: { type: 'line', lineStyle: { color: ink.crosshair, width: 1 } },
        backgroundColor: ink.tooltipBg,
        borderColor: ink.tooltipBorder,
        borderWidth: 1,
        padding: [6, 10],
        textStyle: { color: ink.tooltipText, fontSize: 12 },
        valueFormatter: (v: unknown) => formatMetricValue(Number(v)),
      },
      series: lines,
    }
  }, [theme, series, interval])

  const ref = useECharts(option)

  return <div ref={ref} className="h-64 w-full" role="img" aria-label="Metric time series chart" />
}

export function MetricChartSkeleton() {
  return <Skeleton className="h-64 w-full" />
}
