<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../api'

interface InboxItem {
  id: number
  job_uuid: string
  caller_number: string
  caller_name: string
  callee_number: string
  number: string
  number_name: string
  pages: number
  file_bytes: number
  received_at: string
}

const faxes = ref<InboxItem[]>([])
const timer = ref<number | null>(null)
const error = ref('')

async function load() {
  try {
    faxes.value = await api<InboxItem[]>('/inbox?limit=100')
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
}

onMounted(() => {
  load()
  timer.value = window.setInterval(load, 5000)
})
onUnmounted(() => { if (timer.value) clearInterval(timer.value) })

function fmt(ts: string | null): string {
  return ts ? new Date(ts).toLocaleString() : '—'
}

function fmtSize(bytes: number): string {
  if (!bytes) return '—'
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function fileUrl(id: number): string {
  return `/portal/api/inbox/${id}/file`
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Inbox <span class="muted">(received faxes · auto-refreshes)</span></h2>
      <table v-if="faxes.length">
        <thead>
          <tr><th>Received</th><th>From</th><th>To</th><th>Pages</th><th>Size</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="f in faxes" :key="f.id">
            <td>{{ fmt(f.received_at) }}</td>
            <td>{{ f.caller_name ? `${f.caller_name} (${f.caller_number})` : f.caller_number || '—' }}</td>
            <td>{{ f.number_name ? `${f.number_name} (${f.number})` : f.number || f.callee_number }}</td>
            <td>{{ f.pages || '—' }}</td>
            <td>{{ fmtSize(f.file_bytes) }}</td>
            <td class="actions-cell">
              <a :href="fileUrl(f.id)" target="_blank" rel="noopener"><button>View PDF</button></a>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted">No received faxes yet.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
