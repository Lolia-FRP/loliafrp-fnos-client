<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string, string> = {
  home: 'm3 10 9-7 9 7 M5 9v12h14V9 M9 21v-8h6v8',
  server: 'M4 3h16v7H4z M4 14h16v7H4z M7 6h.01 M7 17h.01 M11 6h6 M11 17h6',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0 M3 12h18 M12 3a18 18 0 0 1 0 18 18 18 0 0 1 0-18',
  nodes: 'M9 3h6v6H9z M2 16h6v6H2z M16 16h6v6h-6z M12 9v4 M5 16v-3h14v3',
  file: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z M14 2v6h6 M8 13h8 M8 17h8',
  settings: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1z',
  login: 'M9 3h10v18H9 M3 12h12 M11 8l4 4-4 4',
  logout: 'M9 3H4v18h5 M10 12h11 M17 8l4 4-4 4',
  help: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0 M9.5 9a2.5 2.5 0 1 1 4 2c-1.5 1-1.5 1.5-1.5 2 M12 17h.01',
  user: 'M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0 M5 21v-2a7 7 0 0 1 14 0v2',
  chart: 'M4 3v17h17 M7 14l5-5 4 3 5-7',
  info: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0 M12 11v6 M12 7h.01',
  traffic: 'M8 20V4 M4 8l4-4 4 4 M16 4v16 M12 16l4 4 4-4',
}
</script>

<template>
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] || paths.server" /></svg>
</template>
