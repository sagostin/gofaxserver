<script lang="ts">
// FaxLeg is one call leg / attempt of a fax job — mirrors gofaxserver's
// fax_job_results row as returned by the portal's /admin/fax-results
// (grouped legs) and /admin/inbox/{id}/attempts endpoints.
export interface FaxLeg {
  id: number
  call_uuid: string
  result_type: string // reception | bridge | transmission | delivery | submission
  attempt_number: number
  endpoint_type: string
  gateway: string // actual FS gateway used (transmission: winning gw; reception/bridge: arrival gw)
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
  transfer_rate: number
  remote_id: string
  status: string
  start_ts: string | null
  end_ts: string | null
  created_at: string
  ecm?: boolean
  ecm_requested?: boolean
  v17_disabled?: boolean
}
</script>

<script setup lang="ts">
import { ref } from 'vue'

defineProps<{ legs: FaxLeg[] }>()

// Expanded legs, keyed by row id.
const open = ref<Record<number, boolean>>({})
const copied = ref('')

function toggle(id: number) {
  open.value[id] = !open.value[id]
}

async function copyUuid(u: string) {
  if (!u) return
  try {
    await navigator.clipboard.writeText(u)
    copied.value = u
    setTimeout(() => { if (copied.value === u) copied.value = '' }, 1500)
  } catch { /* clipboard unavailable */ }
}

function fmtWhen(ts: string | null): string {
  return ts ? new Date(ts).toLocaleString() : '—'
}

function duration(start: string | null, end: string | null): string {
  if (!start || !end) return ''
  const ms = new Date(end).getTime() - new Date(start).getTime()
  if (isNaN(ms) || ms < 0) return ''
  if (ms < 1000) return `${ms} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} s`
  const m = Math.floor(s / 60)
  return `${m} m ${Math.round(s % 60)} s`
}

function fmtRate(l: FaxLeg): string {
  const r = l.transfer_rate || l.signal_rate
  return r ? `${r.toLocaleString()} bps` : ''
}

function fmtPages(l: FaxLeg): string {
  if (!l.transferred_pages && !l.total_pages) return ''
  return `${l.transferred_pages}/${l.total_pages || '?'} pages`
}

// submissionMarker is the internal placeholder value gofaxserver stamps on
// submission (intake) legs — not a real hangup cause or outcome.
const submissionMarker = 'WEBHOOK'

// summary assembles the one-line fact list for a leg, skipping empty fields.
function summary(l: FaxLeg): string {
  const facts: string[] = []
  if (l.is_bridge) {
    facts.push(`${l.bridge_direction || 'bridge'} via ${l.bridge_gateway || '—'}`)
  } else if (l.gateway) {
    facts.push(`via ${l.gateway}`)
  } else if (l.endpoint_type) {
    facts.push(`endpoint ${l.endpoint_type}`)
  }
  const pages = fmtPages(l)
  if (pages) facts.push(pages)
  const rate = fmtRate(l)
  if (rate) facts.push(rate)
  if (l.t38_status) facts.push(`T.38 ${l.t38_status}`)
  else if (l.used_t38) facts.push('T.38')
  if (l.hangup_cause && l.hangup_cause !== submissionMarker) facts.push(l.hangup_cause)
  if (l.result_text && l.result_text !== l.hangup_cause) facts.push(l.result_text)
  if (l.status && l.status !== l.result_text && l.status !== submissionMarker) facts.push(l.status)
  return facts.join(' · ')
}

// detailRows builds the expanded key/value grid, omitting empty fields.
function detailRows(l: FaxLeg): { k: string; v: string; copy?: string }[] {
  const rows: { k: string; v: string; copy?: string }[] = []
  if (l.call_uuid) rows.push({ k: 'Call UUID', v: l.call_uuid, copy: l.call_uuid })
  if (l.is_bridge) {
    rows.push({ k: 'Bridge', v: `${l.bridge_direction || '—'} via ${l.bridge_gateway || '—'}` })
  } else if (l.endpoint_type) {
    rows.push({ k: 'Endpoint', v: l.endpoint_type })
  }
  if (l.gateway) {
    const label = l.result_type === 'reception' || l.result_type === 'bridge' ? 'Arrival gateway' : 'Gateway'
    rows.push({ k: label, v: l.gateway })
  }
  const pages = fmtPages(l)
  if (pages) rows.push({ k: 'Pages', v: pages })
  const rate = fmtRate(l)
  if (rate) rows.push({ k: 'Rate', v: rate })
  if (l.t38_status) rows.push({ k: 'T.38', v: l.t38_status })
  else if (l.used_t38) rows.push({ k: 'T.38', v: 'enabled' })
  if (l.ecm) rows.push({ k: 'ECM', v: 'used' })
  else if (l.ecm_requested) rows.push({ k: 'ECM', v: 'requested' })
  if (l.v17_disabled) rows.push({ k: 'V.17', v: 'disabled' })
  if (l.remote_id) rows.push({ k: 'Remote ID', v: l.remote_id })
  if (l.hangup_cause && l.hangup_cause !== submissionMarker) rows.push({ k: 'Hangup cause', v: l.hangup_cause })
  if (l.result_text) rows.push({ k: 'Result', v: l.result_text })
  if (l.status && l.status !== submissionMarker) rows.push({ k: 'Status', v: l.status })
  if (l.start_ts) rows.push({ k: 'Started', v: fmtWhen(l.start_ts) })
  if (l.end_ts) rows.push({ k: 'Ended', v: fmtWhen(l.end_ts) })
  const d = duration(l.start_ts, l.end_ts)
  if (d) rows.push({ k: 'Duration', v: d })
  rows.push({ k: 'Recorded', v: fmtWhen(l.created_at) })
  return rows
}
</script>

<template>
  <div class="legs">
    <div v-for="leg in legs" :key="leg.id" class="leg">
      <button type="button" class="leg-head" @click="toggle(leg.id)">
        <span class="chip">#{{ leg.attempt_number }}</span>
        <span class="badge" :class="leg.result_type">{{ leg.result_type }}</span>
        <span v-if="leg.result_type === 'submission'" class="badge" :class="leg.success ? 'processed' : 'queued'">{{ leg.success ? 'processed' : 'queued' }}</span>
        <span v-else class="badge" :class="leg.success ? 'success' : 'failed'">{{ leg.success ? 'success' : 'failed' }}</span>
        <span class="leg-summary">{{ summary(leg) }}</span>
        <span class="leg-when muted">
          <template v-if="duration(leg.start_ts, leg.end_ts)">{{ duration(leg.start_ts, leg.end_ts) }} · </template>{{ fmtWhen(leg.created_at) }}
        </span>
        <span class="leg-caret">{{ open[leg.id] ? '▾' : '▸' }}</span>
      </button>
      <div v-if="open[leg.id]" class="leg-detail">
        <div class="kv-grid">
          <div v-for="r in detailRows(leg)" :key="r.k">
            <div class="k">{{ r.k }}</div>
            <div v-if="r.copy" class="v copyable" :title="copied === r.copy ? 'Copied!' : 'Click to copy'" @click="copyUuid(r.copy)">
              {{ copied === r.copy ? '✓ copied' : r.v }}
            </div>
            <div v-else class="v">{{ r.v }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
