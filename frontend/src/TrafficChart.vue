<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'

const props = defineProps<{ days: { date: string; total_traffic: number }[] }>()
const host = ref<HTMLElement>()
const width = ref(800)
let observer: ResizeObserver | undefined
const unit = computed(() => {
  const max = Math.max(0, ...props.days.map(d => d.total_traffic))
  const index = Math.min(4, Math.max(0, Math.floor(Math.log(Math.max(1, max)) / Math.log(1024))))
  return { label: ['B', 'KB', 'MB', 'GB', 'TB'][index], divisor: 1024 ** index }
})
const ceiling = computed(() => {
  const max = Math.max(0, ...props.days.map(d => d.total_traffic / unit.value.divisor))
  return max > 0 ? Math.ceil(max / 4) * 4 : 4
})
const points = computed(() => props.days.map((day, i) => ({
  ...day,
  x: 42 + i * (width.value - 60) / Math.max(1, props.days.length - 1),
  y: 185 - Math.max(0, day.total_traffic / unit.value.divisor) / ceiling.value * 170,
})))
const line = computed(() => points.value.map((p, i) => `${i ? 'L' : 'M'}${p.x},${p.y}`).join(' '))
const area = computed(() => points.value.length ? `${line.value} L${points.value.at(-1)!.x},185 L42,185 Z` : '')
onMounted(() => {
  observer = new ResizeObserver(([entry]) => { if (entry) width.value = Math.max(220, entry.contentRect.width) })
  if (host.value) observer.observe(host.value)
})
onUnmounted(() => observer?.disconnect())
</script>

<template>
  <div ref="host" class="traffic-chart">
    <span class="chart-unit">近 7 天 / {{ unit.label }}</span>
    <svg :viewBox="`0 0 ${width} 225`" role="img" aria-label="最近七天流量趋势" width="100%" height="225">
      <g v-for="i in 5" :key="i" class="chart-grid">
        <line x1="42" :x2="width - 18" :y1="15 + (i - 1) * 42.5" :y2="15 + (i - 1) * 42.5" />
        <text x="30" :y="19 + (i - 1) * 42.5" text-anchor="end">{{ ((5 - i) * ceiling / 4).toLocaleString() }}</text>
      </g>
      <path v-if="area" :d="area" class="chart-area" />
      <path v-if="line" :d="line" class="chart-line" />
      <g v-for="(p, i) in points" :key="p.date">
        <circle :cx="p.x" :cy="p.y" r="3" class="chart-point"><title>{{ p.date.slice(0, 10) }}：{{ (p.total_traffic / unit.divisor).toFixed(2) }} {{ unit.label }}</title></circle>
        <text :x="p.x" y="214" :text-anchor="i === 0 ? 'start' : i === points.length - 1 ? 'end' : 'middle'" class="chart-date">{{ p.date.slice(5, 10) }}</text>
      </g>
    </svg>
  </div>
</template>
