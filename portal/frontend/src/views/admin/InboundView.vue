<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org { id: number; name: string }
interface InboundItem {
  id: number
  job_uuid: string
  org_id: number
  number_id: number | null
  caller_number: string
  caller_name: string
  callee_number: string
  number: string
  number_name: string
  pages: number
  file_bytes: number
  received_at: string
}

const faxes = ref<InboundItem[]>([])
const orgs = ref<Org[]>([])
const orgFilter = ref(0)
const error = ref('')

async function load() {
  try {
    const q = orgFilter.value > 0 ? `?org_id=${orgFilter.value}&limit=200` : '?limit=200'
    faxes.value = await api<InboundItem[]>(`/admin/inbox${q}`)
    orgs.value = await api<Org[]>('/admin/orgs')
    error.value = ''
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function remove(f: InboundItem) {
  if (!confirm(`Delete received fax ${f.job_uuid}? This removes the stored PDF permanently.`)) return
  error.value = ''
  try {
    await api(`/admin/inbox/${f.id}`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

function orgName(id: number): string {
  return orgs.value.find((o) => o.id === id)?.name || `#${id}`
}

function fmt(ts: string | null): string {
  return ts ? new Date(ts).toLocaleString() : '—'
}

function fmtSize(bytes: number): string {
  if (!bytes) return '—'
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function fileUrl(id: number): string {
  return `/portal/api/admin/inbox/${id}/file`
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Inbound faxes</h2>
      <div class="inline" style="margin-bottom:10px">
        <div>
          <label>Organization</label>
          <select v-model="orgFilter" @change="load">
            <option :value="0">All</option>
            <option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </div>
        <button class="secondary" @click="load">Refresh</button>
      </div>
      <table v-if="faxes.length">
        <thead>
          <tr><th>Received</th><th>Org</th><th>From</th><th>To</th><th>Pages</th><th>Size</th><th>Job UUID</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="f in faxes" :key="f.id">
            <td>{{ fmt(f.received_at) }}</td>
            <td>{{ orgName(f.org_id) }}</td>
            <td>{{ f.caller_name ? `${f.caller_name} (${f.caller_number})` : f.caller_number || '—' }}</td>
            <td>
              {{ f.number_name ? `${f.number_name} (${f.number})` : f.number || f.callee_number }}
              <span v-if="!f.number_id" class="badge inactive" title="Number not mirrored in portal; visible to admins only">unmatched</span>
            </td>
            <td>{{ f.pages || '—' }}</td>
            <td>{{ fmtSize(f.file_bytes) }}</td>
            <td class="muted" style="font-size:11px">{{ f.job_uuid }}</td>
            <td class="actions-cell">
              <a :href="fileUrl(f.id)" target="_blank" rel="noopener"><button>View PDF</button></a>
              <button class="danger" @click="remove(f)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted">No inbound faxes stored.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
