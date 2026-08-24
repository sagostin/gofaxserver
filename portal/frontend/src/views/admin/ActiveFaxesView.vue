<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../api'

interface Run {
  job_uuid: string
  phase: string
  caller: string
  callee: string
  attempt: number
  last_result: string
}

const items = ref<Run[]>([])
const active = ref(0)
const timer = ref<number | null>(null)
const error = ref('')

async function load() {
  try {
    const d = await api<{ active: number; items: Run[] }>('/admin/faxes/active')
    items.value = d.items || []
    active.value = d.active || 0
    error.value = ''
  } catch (e: any) { error.value = e.message }
}
onMounted(() => { load(); timer.value = window.setInterval(load, 3000) })
onUnmounted(() => { if (timer.value) clearInterval(timer.value) })
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Active faxes <span class="badge sending">{{ active }} in flight</span></h2>
      <table v-if="items.length">
        <thead><tr><th>Job</th><th>Phase</th><th>From → To</th><th>Attempt</th><th>Last result</th></tr></thead>
        <tbody>
          <tr v-for="r in items" :key="r.job_uuid">
            <td class="muted">{{ r.job_uuid.slice(0, 8) }}…</td>
            <td>{{ r.phase }}</td>
            <td>{{ r.caller }} → {{ r.callee }}</td>
            <td>{{ r.attempt }}</td>
            <td>{{ r.last_result || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted">Nothing in flight right now.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
