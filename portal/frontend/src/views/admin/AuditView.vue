<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Entry {
  id: number
  actor_username: string
  action: string
  target: string
  detail: string
  ip: string
  created_at: string
}

const logs = ref<Entry[]>([])
const action = ref('')
const error = ref('')

async function load() {
  try {
    const q = action.value ? `?action=${encodeURIComponent(action.value)}` : ''
    logs.value = await api<Entry[]>('/admin/audit' + q)
    error.value = ''
  } catch (e: any) { error.value = e.message }
}
onMounted(load)
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Audit log</h2>
      <form class="inline" @submit.prevent="load">
        <div><label>Action filter (e.g. FAX_SEND)</label><input v-model="action" /></div>
        <button class="secondary">Apply</button>
      </form>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
    <div class="panel">
      <table>
        <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Target</th><th>Detail</th><th>IP</th></tr></thead>
        <tbody>
          <tr v-for="l in logs" :key="l.id">
            <td>{{ new Date(l.created_at).toLocaleString() }}</td>
            <td>{{ l.actor_username || '—' }}</td>
            <td>{{ l.action }}</td>
            <td class="muted">{{ l.target }}</td>
            <td class="muted" style="max-width:340px;overflow-wrap:anywhere">{{ l.detail }}</td>
            <td class="muted">{{ l.ip }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="!logs.length" class="muted">No entries.</p>
    </div>
  </main>
</template>
