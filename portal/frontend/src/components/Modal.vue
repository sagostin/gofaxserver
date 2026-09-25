<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

function onOverlay(e: MouseEvent) {
  if (e.target === e.currentTarget) emit('close')
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="modal-overlay" @mousedown="onOverlay">
    <div class="modal" role="dialog" :aria-label="title">
      <div class="modal-head">
        <h3>{{ title }}</h3>
        <button type="button" class="secondary" title="Close" @click="emit('close')">✕</button>
      </div>
      <div class="modal-body"><slot /></div>
      <div v-if="$slots.footer" class="modal-actions"><slot name="footer" /></div>
    </div>
  </div>
</template>
