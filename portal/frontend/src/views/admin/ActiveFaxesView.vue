<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../api'

interface Run {
  job_uuid: string
  phase: string
  src_tenant_id: number
  dst_tenant_id: number
  src_tenant_name: string
  dst_tenant_name: string
  caller: string
  callee: string
  attempt: number
  max_attempts: number
  endpoint_type: string
  endpoint_label: string
  endpoint_value: string
  last_result: string
  enqueued_at: string
  started_at: string
  updated_at: string
}

const items = ref<Run[]>([])
const active = ref(0)
const timer = ref<number | null>(null)
const error = ref('')
const tenantFilter = ref('')
const phaseFilter = ref('')

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

const tenantNames = computed(() => {
  const s = new Set<string>()
  for (const r of items.value) {
    if (r.src_tenant_name) s.add(r.src_tenant_name)
    if (r.dst_tenant_name) s.add(r.dst_tenant_name)
  }
  return [...s].sort()
})

const phases = computed(() => [...new Set(items.value.map((r) => r.phase))].sort())

const filtered = computed(() => items.value.filter((r) => {
  if (phaseFilter.value && r.phase !== phaseFilter.value) return false
  if (tenantFilter.value && r.src_tenant_name !== tenantFilter.value && r.dst_tenant_name !== tenantFilter.value) return false
  return true
}))

function tenant(r: Run): string {
  const src = r.src_tenant_name || (r.src_tenant_id ? `#${r.src_tenant_id}` : '—')
  const dst = r.dst_tenant_name || (r.dst_tenant_id ? `#${r.dst_tenant_id}` : '—')
  return `${src} → ${dst}`
}

function endpoint(r: Run): string {
  const label = r.endpoint_label || r.endpoint_type
  return r.endpoint_value ? `${label} (${r.endpoint_value})` : label || '—'
}

function elapsed(r: Run): string {
  const start = r.started_at || r.enqueued_at
  if (!start) return '—'
  const s = Math.max(0, Math.floor((Date.now() - new Date(start).getTime()) / 1000))
  if (s < 60) return `${s}s`
  return `${Math.floor(s / 60)}m${s % 60}s`
}

function phaseBadge(phase: string): string {
  switch (phase) {
    case 'SENDING': case 'ATTEMPTING': return 'sending'
    case 'RECEIVING': case 'BRIDGING': return 'active'
    case 'WAITING': return 'queued'
    default: return 'inactive'
  }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Active faxes <span class="badge sending">{{ active }} in flight</span></h2>
      <div class="inline" style="margin-bottom:10px">
        <div>
          <label>Tenant</label>
          <select v-model="tenantFilter">
            <option value="">All</option>
            <option v-for="t in tenantNames" :key="t" :value="t">{{ t }}</option>
          </select>
        </div>
        <div>
          <label>Phase</label>
          <select v-model="phaseFilter">
            <option value="">All</option>
            <option v-for="p in phases" :key="p" :value="p">{{ p }}</option>
          </select>
        </div>
        <button class="secondary" @click="load">Refresh</button>
      </div>
      <table v-if="filtered.length">
        <thead>
          <tr><th>Job</th><th>Phase</th><th>Tenant (src → dst)</th><th>From → To</th><th>Endpoint / Gateway</th><th>Attempt</th><th>Elapsed</th><th>Last result</th></tr>
        </thead>
        <tbody>
          <tr v-for="r in filtered" :key="r.job_uuid">
            <td class="muted" :title="r.job_uuid">{{ r.job_uuid.slice(0, 8) }}…</td>
            <td><span class="badge" :class="phaseBadge(r.phase)">{{ r.phase }}</span></td>
            <td>{{ tenant(r) }}</td>
            <td>{{ r.caller }} → {{ r.callee }}</td>
            <td>{{ endpoint(r) }}</td>
            <td>{{ r.attempt }}<span v-if="r.max_attempts" class="muted">/{{ r.max_attempts }}</span></td>
            <td>{{ elapsed(r) }}</td>
            <td>{{ r.last_result || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted">Nothing in flight right now.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
