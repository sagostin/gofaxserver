<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org { id: number; name: string; gofax_tenant_id: number }
interface Tenant { id: number; name: string }
interface Job {
  id: number
  job_uuid: string
  username: string
  org_name: string
  caller_number: string
  callee_number: string
  original_filename: string
  status: string
  pages: number
  attempts: number
  result_text: string
  last_error: string
  submitted_at: string
}
interface ResultRow {
  id: number
  job_uuid: string
  src_tenant_id: number
  dst_tenant_id: number
  src_tenant_name: string
  dst_tenant_name: string
  caller_id_number: string
  caller_id_name: string
  callee_number: string
  result_type: string
  attempt_number: number
  endpoint_type: string
  success: boolean
  transferred_pages: number
  total_pages: number
  hangup_cause: string
  result_text: string
  t38_status: string
  used_t38: boolean
  is_bridge: boolean
  bridge_direction: string
  bridge_gateway: string
  signal_rate: number
  remote_id: string
  status: string
  start_ts: string | null
  end_ts: string | null
  created_at: string
}
interface ResultList { total: number; items: ResultRow[] }

const view = ref<'portal' | 'all'>('portal')
const error = ref('')

// --- portal jobs state ---
const jobs = ref<Job[]>([])
const orgs = ref<Org[]>([])
const orgFilter = ref(0)
const statusFilter = ref('')

// --- all fax results state ---
const rows = ref<ResultRow[]>([])
const total = ref(0)
const page = ref(0)
const pageSize = 50
const tenants = ref<Tenant[]>([])
const tenantFilter = ref(0)
const typeFilter = ref('')
const successFilter = ref('')
const numberSearch = ref('')
const uuidSearch = ref('')
const fromDate = ref('')
const toDate = ref('')

// Expanded detail/attempt rows (per view), keyed by row id / job uuid.
const expanded = ref<Record<string, ResultRow[]>>({})
const expandedMeta = ref<Record<number, boolean>>({})

async function loadOrgsTenants() {
  try { orgs.value = await api<Org[]>('/admin/orgs') } catch { /* non-fatal */ }
  try { tenants.value = await api<Tenant[]>('/admin/upstream/tenants') } catch { /* non-fatal */ }
}

async function loadPortalJobs() {
  try {
    const qs = new URLSearchParams()
    if (orgFilter.value) qs.set('org_id', String(orgFilter.value))
    if (statusFilter.value) qs.set('status', statusFilter.value)
    jobs.value = await api<Job[]>('/admin/jobs?' + qs.toString())
    error.value = ''
  } catch (e: any) { error.value = e.message }
}

async function loadAllResults() {
  try {
    const qs = new URLSearchParams({ limit: String(pageSize), offset: String(page.value * pageSize) })
    if (tenantFilter.value) qs.set('tenant_id', String(tenantFilter.value))
    if (typeFilter.value) qs.set('result_type', typeFilter.value)
    if (successFilter.value) qs.set('success', successFilter.value)
    if (numberSearch.value.trim()) qs.set('number', numberSearch.value.trim())
    if (uuidSearch.value.trim()) qs.set('job_uuid', uuidSearch.value.trim())
    if (fromDate.value) qs.set('from', fromDate.value)
    if (toDate.value) qs.set('to', toDate.value)
    const d = await api<ResultList>('/admin/fax-results?' + qs.toString())
    rows.value = d.items || []
    total.value = d.total || 0
    expandedMeta.value = {}
    error.value = ''
  } catch (e: any) { error.value = e.message }
}

function load() { view.value === 'portal' ? loadPortalJobs() : loadAllResults() }
function switchView(v: 'portal' | 'all') {
  view.value = v
  expanded.value = {}
  expandedMeta.value = {}
  load()
}
onMounted(() => { loadOrgsTenants(); load() })

function searchAll() { page.value = 0; loadAllResults() }
function nextPage() { page.value++; loadAllResults() }
function prevPage() { if (page.value > 0) { page.value--; loadAllResults() } }

// Expand a portal job to show its upstream attempts (persisted results).
async function toggleAttempts(j: Job) {
  if (j.job_uuid in expanded.value) {
    delete expanded.value[j.job_uuid]
    return
  }
  try {
    const d = await api<ResultList>('/admin/fax-results?job_uuid=' + encodeURIComponent(j.job_uuid))
    expanded.value[j.job_uuid] = d.items || []
  } catch (e: any) { error.value = e.message }
}

function toggleMeta(id: number) {
  expandedMeta.value[id] = !expandedMeta.value[id]
}

function tenantLabel(id: number): string {
  if (!id) return '—'
  const org = orgs.value.find((o) => o.gofax_tenant_id === id)
  if (org) return org.name
  const t = tenants.value.find((t) => t.id === id)
  return t ? `${t.name} (#${id})` : `#${id}`
}

function fmt(ts: string | null): string {
  return ts ? new Date(ts).toLocaleString() : '—'
}

function typeBadge(r: ResultRow): string {
  if (r.result_type === 'bridge') return 'sending'
  if (r.result_type === 'reception') return 'queued'
  return 'active'
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Fax jobs</h2>
      <div class="inline" style="margin-bottom:10px">
        <button :class="view === 'portal' ? '' : 'secondary'" @click="switchView('portal')">Portal jobs</button>
        <button :class="view === 'all' ? '' : 'secondary'" @click="switchView('all')">All fax results</button>
      </div>

      <!-- ===== Portal-submitted jobs ===== -->
      <template v-if="view === 'portal'">
        <form class="inline" @submit.prevent="loadPortalJobs">
          <div>
            <label>Org</label>
            <select v-model="orgFilter"><option :value="0">All</option><option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option></select>
          </div>
          <div>
            <label>Status</label>
            <select v-model="statusFilter">
              <option value="">All</option><option>queued</option><option>sending</option><option>success</option><option>failed</option>
            </select>
          </div>
          <button class="secondary">Apply</button>
        </form>

        <table style="margin-top:10px">
          <thead>
            <tr><th>Submitted</th><th>User / Org</th><th>Doc</th><th>From → To</th><th>Status</th><th>Pages</th><th>Att.</th><th>Result</th><th></th></tr>
          </thead>
          <tbody>
            <template v-for="j in jobs" :key="j.id">
              <tr>
                <td>{{ fmt(j.submitted_at) }}</td>
                <td>{{ j.username }} / {{ j.org_name }}</td>
                <td>{{ j.original_filename }}</td>
                <td>{{ j.caller_number }} → {{ j.callee_number }}</td>
                <td><span class="badge" :class="j.status">{{ j.status }}</span></td>
                <td>{{ j.pages || '—' }}</td>
                <td>{{ j.attempts }}</td>
                <td>{{ j.result_text || j.last_error || '—' }}</td>
                <td><button class="secondary" @click="toggleAttempts(j)">{{ j.job_uuid in expanded ? 'Hide' : 'Attempts' }}</button></td>
              </tr>
              <tr v-if="j.job_uuid in expanded">
                <td colspan="9">
                  <table v-if="expanded[j.job_uuid].length" style="margin:6px 0">
                    <thead>
                      <tr><th>#</th><th>Type</th><th>Success</th><th>Pages</th><th>T.38</th><th>Cause</th><th>Result</th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="r in expanded[j.job_uuid]" :key="r.id">
                        <td>{{ r.attempt_number }}</td>
                        <td><span class="badge" :class="typeBadge(r)">{{ r.result_type }}</span></td>
                        <td><span class="badge" :class="r.success ? 'success' : 'failed'">{{ r.success ? 'yes' : 'no' }}</span></td>
                        <td>{{ r.transferred_pages }}/{{ r.total_pages || '—' }}</td>
                        <td>{{ r.t38_status || '—' }}</td>
                        <td>{{ r.hangup_cause || '—' }}</td>
                        <td>{{ r.result_text || '—' }}</td>
                      </tr>
                    </tbody>
                  </table>
                  <p v-else class="muted">No upstream attempts recorded yet.</p>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </template>

      <!-- ===== All upstream fax results ===== -->
      <template v-else>
        <form class="inline" @submit.prevent="searchAll">
          <div>
            <label>Tenant</label>
            <select v-model="tenantFilter">
              <option :value="0">All</option>
              <option v-for="t in tenants" :key="t.id" :value="t.id">{{ t.name }} (#{{ t.id }})</option>
            </select>
          </div>
          <div>
            <label>Type</label>
            <select v-model="typeFilter">
              <option value="">All</option>
              <option value="transmission">transmission</option>
              <option value="reception">reception</option>
              <option value="bridge">bridge</option>
              <option value="delivery">delivery</option>
            </select>
          </div>
          <div>
            <label>Success</label>
            <select v-model="successFilter">
              <option value="">All</option>
              <option value="true">success</option>
              <option value="false">failed</option>
            </select>
          </div>
          <div>
            <label>Number</label>
            <input v-model="numberSearch" placeholder="caller or callee" />
          </div>
          <div>
            <label>Job UUID</label>
            <input v-model="uuidSearch" placeholder="exact uuid" style="width:220px" />
          </div>
          <div>
            <label>From</label>
            <input v-model="fromDate" type="date" />
          </div>
          <div>
            <label>To</label>
            <input v-model="toDate" type="date" />
          </div>
          <button class="secondary" type="submit">Apply</button>
        </form>

        <table style="margin-top:10px">
          <thead>
            <tr><th>Time</th><th>Type</th><th>From → To</th><th>Tenant (src → dst)</th><th>Success</th><th>Pages</th><th>Cause / Result</th><th></th></tr>
          </thead>
          <tbody>
            <template v-for="r in rows" :key="r.id">
              <tr>
                <td>{{ fmt(r.created_at) }}</td>
                <td><span class="badge" :class="typeBadge(r)">{{ r.result_type }}</span></td>
                <td>{{ r.caller_id_number || '—' }} → {{ r.callee_number || '—' }}</td>
                <td>{{ tenantLabel(r.src_tenant_id) }} → {{ tenantLabel(r.dst_tenant_id) }}</td>
                <td><span class="badge" :class="r.success ? 'success' : 'failed'">{{ r.success ? 'yes' : 'no' }}</span></td>
                <td>{{ r.transferred_pages }}/{{ r.total_pages || '—' }}</td>
                <td>{{ r.hangup_cause || r.result_text || '—' }}</td>
                <td><button class="secondary" @click="toggleMeta(r.id)">{{ expandedMeta[r.id] ? 'Hide' : 'Details' }}</button></td>
              </tr>
              <tr v-if="expandedMeta[r.id]">
                <td colspan="8" class="muted" style="font-size:12px">
                  <div style="display:flex; gap:24px; flex-wrap:wrap; padding:4px 0">
                    <span><b>Job:</b> {{ r.job_uuid }}</span>
                    <span><b>Attempt:</b> {{ r.attempt_number }}</span>
                    <span v-if="r.is_bridge"><b>Bridge:</b> {{ r.bridge_direction || '—' }} via {{ r.bridge_gateway || '—' }}</span>
                    <span v-else><b>Endpoint:</b> {{ r.endpoint_type || '—' }}</span>
                    <span><b>T.38:</b> {{ r.t38_status || (r.used_t38 ? 'used' : '—') }}</span>
                    <span><b>Rate:</b> {{ r.signal_rate || '—' }}</span>
                    <span><b>Remote ID:</b> {{ r.remote_id || '—' }}</span>
                    <span><b>Started:</b> {{ fmt(r.start_ts) }}</span>
                    <span><b>Ended:</b> {{ fmt(r.end_ts) }}</span>
                    <span><b>Status:</b> {{ r.status || '—' }}</span>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
        <p v-if="!rows.length" class="muted">No fax results match.</p>
        <div v-if="total > pageSize" class="inline" style="margin-top:10px; align-items:center">
          <button class="secondary" :disabled="page === 0" @click="prevPage">← Prev</button>
          <span class="muted">Page {{ page + 1 }} of {{ Math.ceil(total / pageSize) }} ({{ total }} results)</span>
          <button class="secondary" :disabled="(page + 1) * pageSize >= total" @click="nextPage">Next →</button>
        </div>
      </template>

      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
