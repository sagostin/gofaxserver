<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../api'

interface Job {
  id: number
  job_uuid: string
  caller_number: string
  callee_number: string
  original_filename: string
  status: string
  pages: number
  total_pages: number
  attempts: number
  result_text: string
  last_error: string
  submitted_at: string
  completed_at: string | null
}

const jobs = ref<Job[]>([])
const timer = ref<number | null>(null)
const error = ref('')
const loaded = ref(false)

async function load() {
  try {
    jobs.value = await api<Job[]>('/faxes?limit=100')
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  } finally {
    loaded.value = true
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

// fmtPages shows transferred/total for finished jobs (a "failed" fax whose
// pages all crossed reads 15/15). Queued/sending jobs have transferred
// nothing yet — show the intake document total without a misleading "0/N"
// ratio (0 total = unknown).
function fmtPages(j: Job): string {
  if (j.status === 'queued' || j.status === 'sending') {
    return j.total_pages ? `${j.total_pages} pages` : '—'
  }
  if (!j.pages && !j.total_pages) return '—'
  if (j.total_pages) return `${j.pages}/${j.total_pages}`
  return `${j.pages}`
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>My faxes <span class="muted">(auto-refreshes)</span></h2>
      <table v-if="jobs.length">
        <thead>
          <tr><th>Submitted</th><th>Document</th><th>From → To</th><th>Status</th><th>Pages</th><th>Attempts</th><th>Result</th></tr>
        </thead>
        <tbody>
          <tr v-for="j in jobs" :key="j.id">
            <td>{{ fmt(j.submitted_at) }}</td>
            <td>{{ j.original_filename }}</td>
            <td>{{ j.caller_number }} → {{ j.callee_number }}</td>
            <td><span class="badge" :class="j.status">{{ j.status }}</span></td>
            <td>{{ fmtPages(j) }}</td>
            <td>{{ j.attempts }}</td>
            <td>{{ j.result_text || j.last_error || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else-if="!loaded" class="muted">Loading…</p>
      <p v-else class="muted">No faxes yet.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
