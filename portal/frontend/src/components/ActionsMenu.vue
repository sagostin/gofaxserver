<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

export interface ActionItem {
  label: string
  danger?: boolean
  hint?: string
  onClick: () => void
}

const props = withDefaults(defineProps<{ items: ActionItem[]; label?: string }>(), { label: 'Actions' })

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const normal = computed(() => props.items.filter((i) => !i.danger))
const dangerous = computed(() => props.items.filter((i) => i.danger))

function run(item: ActionItem) {
  open.value = false
  item.onClick()
}
function onDocClick(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('mousedown', onDocClick)
  document.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick)
  document.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div ref="root" class="actions-menu">
    <button type="button" class="secondary" @click="open = !open">{{ label }} ▾</button>
    <div v-if="open" class="menu" role="menu">
      <button
        v-for="item in normal"
        :key="item.label"
        type="button"
        class="menu-item"
        :title="item.hint"
        @click="run(item)"
      >{{ item.label }}</button>
      <div v-if="normal.length && dangerous.length" class="menu-divider"></div>
      <button
        v-for="item in dangerous"
        :key="item.label"
        type="button"
        class="menu-item danger"
        :title="item.hint"
        @click="run(item)"
      >{{ item.label }}</button>
    </div>
  </div>
</template>
