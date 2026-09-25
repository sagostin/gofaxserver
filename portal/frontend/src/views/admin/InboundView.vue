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
interface InboxList { total: number; items: InboundItem[] }
interface Attempt {
  id: number
  result_type: string
  attempt_number: number
  success: boolean
  transferred_pages: number
  total_pages: number
  hangup_cause: string
  result_text: string
  t38_status: string
  signal_rate: number
  start_ts: string | null
  end_ts: string | null
}

const faxes = ref<InboundItem[]>([])
const total = ref(0)
const orgs = ref<Org[]>([])
const orgFilter = ref(0)
const uuidSearch = ref('')
const numberSearch = ref('')
const page = ref(0)
const pageSize = 50
const error = ref('')
// Expanded attempt rows, keyed by inbox fax id. null = expanded but failed.
const attempts = ref<Record<number, Attempt[] | null>>({})

async function load() {
  try {
    const qs = new URLSearchParams({ limit: String(pageSize), offset: String(page.value * pageSize) })
    if (orgFilter.value > 0) qs.set('org_id', String(orgFilter.value))
    if (uuidSearch.value.trim()) qs.set('uuid', uuidSearch.value.trim())
    if (numberSearch.value.trim()) qs.set('number', numberSearch.value.trim())
    const d = await api<InboxList>(`/admin/inbox?${qs.toString()}`)
    faxes.value = d.items || []
    total.value = d.total || 0
    attempts.value = {}
    error.value = ''
  } catch (e: any) { error.value = e.message }
}
onMounted(async () => {
  await load()
  try { orgs.value = await api<Org[]>('/admin/orgs') } catch { /* non-fatal */ }
})

function search() { page.value = 0; load() }
function nextPage() { page.value++; load() }
function prevPage() { if (page.value > 0) { page.value--; load() } }

async function toggleAttempts(f: InboundItem) {
  if (f.id in attempts.value) {
    delete attempts.value[f.id]
    return
  }
  attempts.value[f.id] = null
  try {
    attempts.value[f.id] = await api<Attempt[]>(`/admin/inbox/${f.id}/attempts`)
  } catch (e: any) {
    delete attempts.value[f.id]
    error.value = e.message
  }
}

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
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Inbound faxes</h2>
      <p class="muted" style="font-size:12px">
        Metadata only — fax contents are visible to the receiving organization's users, never to admins.
      </p>
      <form class="inline" style="margin-bottom:10px" @submit.prevent="search">
        <div>
          <label>Organization</label>
          <select v-model="orgFilter" @change="search">
            <option :value="0">All</option>
            <option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </div>
        <div>
          <label>Job UUID</label>
          <input v-model="uuidSearch" placeholder="exact uuid" style="width:230px" />
        </div>
        <div>
          <label>Number</label>
          <input v-model="numberSearch" placeholder="caller or callee" />
        </div>
        <button class="secondary" type="submit">Search</button>
      </form>
      <table v-if="faxes.length">
        <thead>
          <tr><th>Received</th><th>Org</th><th>From</th><th>To</th><th>Pages</th><th>Size</th><th>Job UUID</th><th></th></tr>
        </thead>
        <tbody>
          <template v-for="f in faxes" :key="f.id">
            <tr>
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
                <button class="secondary" @click="toggleAttempts(f)">
                  {{ f.id in attempts ? 'Hide attempts' : 'Attempts' }}
                </button>
                <button class="danger" @click="remove(f)">Delete</button>
              </td>
            </tr>
            <tr v-if="f.id in attempts">
              <td colspan="8">
                <table v-if="attempts[f.id]?.length" style="margin:6px 0">
                  <thead>
                    <tr><th>#</th><th>Type</th><th>Success</th><th>Pages</th><th>Rate</th><th>T.38</th><th>Started</th><th>Ended</th><th>Cause</th><th>Result</th></tr>
                  </thead>
                  <tbody>
                    <tr v-for="a in attempts[f.id]!" :key="a.id">
                      <td>{{ a.attempt_number }}</td>
                      <td><span class="badge" :class="a.result_type === 'bridge' ? 'sending' : 'queued'">{{ a.result_type }}</span></td>
                      <td><span class="badge" :class="a.success ? 'success' : 'failed'">{{ a.success ? 'yes' : 'no' }}</span></td>
                      <td>{{ a.transferred_pages }}/{{ a.total_pages || '—' }}</td>
                      <td>{{ a.signal_rate || '—' }}</td>
                      <td>{{ a.t38_status || '—' }}</td>
                      <td>{{ fmt(a.start_ts) }}</td>
                      <td>{{ fmt(a.end_ts) }}</td>
                      <td>{{ a.hangup_cause || '—' }}</td>
                      <td>{{ a.result_text || '—' }}</td>
                    </tr>
                  </tbody>
                </table>
                <p v-else class="muted">No upstream attempts recorded for this job.</p>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <p v-else class="muted">No inbound faxes stored.</p>
      <div v-if="total > pageSize" class="inline" style="margin-top:10px; align-items:center">
        <button class="secondary" :disabled="page === 0" @click="prevPage">← Prev</button>
        <span class="muted">Page {{ page + 1 }} of {{ Math.ceil(total / pageSize) }} ({{ total }} faxes)</span>
        <button class="secondary" :disabled="(page + 1) * pageSize >= total" @click="nextPage">Next →</button>
      </div>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
