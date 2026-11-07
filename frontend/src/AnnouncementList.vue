<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import MarkdownIt from 'markdown-it'
import type { Notice } from './api'

const props = defineProps<{ items: Notice[] }>()
const markdown = new MarkdownIt({ html: true, linkify: true, breaks: true })
const purifier = DOMPurify()
purifier.addHook('afterSanitizeAttributes', node => {
  if (node.tagName === 'A' && node.hasAttribute('href')) {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})
const rendered = computed(() => props.items.map(notice => ({
  ...notice,
  html: purifier.sanitize(markdown.render(notice.content), {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select'],
    FORBID_ATTR: ['style', 'target'],
    SANITIZE_NAMED_PROPS: true,
  }),
})))
function actionLink(raw?: string) {
  try { const url = new URL(raw ?? ''); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : '' }
  catch { return '' }
}
</script>

<template>
  <ul class="announcement-list" aria-label="公告列表">
    <li v-for="notice in rendered" :key="notice.id" class="notice">
      <div class="notice-heading"><h3>{{ notice.title }}</h3><time v-if="notice.created_at" :datetime="notice.created_at">{{ notice.created_at.slice(0, 10) }}</time></div>
      <div class="notice-content" v-html="notice.html"></div>
      <a v-if="notice.action_label && actionLink(notice.action_url)" :href="actionLink(notice.action_url)" target="_blank" rel="noopener noreferrer" class="notice-action">{{ notice.action_label }}</a>
    </li>
  </ul>
</template>
