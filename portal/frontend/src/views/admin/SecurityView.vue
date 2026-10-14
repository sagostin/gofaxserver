<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../api'

interface Metrics {
  enabled: boolean
  backend: string
  fail_threshold: number
  fail_window: string
  active_bans: number
  tracked_ips: number
  failures_seen: number
  blocked_hits: number
  bans_created: number
  bans_expired: number
  reconcile_unbans: number
  manual_bans: number
  manual_unbans: number
  enforce_errors: number
  whitelist_skipped: number
}

interface BlockedIP {
  id: number
  ip: string
  reason: string
  source: string
  fail_count: number
  offense_count: number
  banned_at: string
  expires_at?: string | null
  active: boolean
  unbanned_at?: string | null
  unban_reason?: string
}

const metrics = ref<Metrics | null>(null)
const bans = ref<BlockedIP[]>([])
const showHistory = ref(false)
const error = ref('')
const okMsg = ref('')

const banIP = ref('')
const banReason = ref('')
const banDuration = ref('')
const banBusy = ref(false)

let timer: number | undefined

async function load() {
  try {
    const [m, b] = await Promise.all([
      api<Metrics>('/admin/security/status'),
      api<BlockedIP[]>('/admin/security/blocked-ips' + (showHistory.value ? '?all=true' : '')),
    ])
    metrics.value = m
    bans.value = b || []
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
}

function expiry(b: BlockedIP): string {
  if (!b.expires_at) return 'permanent'
  return new Date(b.expires_at).toLocaleString()
}

async function unban(ip: string) {
  if (!confirm(`Unban ${ip}?`)) return
  try {
    await api(`/admin/security/blocked-ips/${encodeURIComponent(ip)}`, { method: 'DELETE' })
    okMsg.value = `Unbanned ${ip}`
    await load()
  } catch (e: any) {
    error.value = e.message
  }
}

async function ban() {
  error.value = ''
  okMsg.value = ''
  banBusy.value = true
  try {
    await api('/admin/security/blocked-ips', {
      method: 'POST',
      json: { ip: banIP.value.trim(), reason: banReason.value.trim(), duration: banDuration.value.trim() },
    })
    okMsg.value = `Banned ${banIP.value.trim()}`
    banIP.value = ''
    banReason.value = ''
    banDuration.value = ''
    await load()
  } catch (e: any) {
    error.value = e.message
  } finally {
    banBusy.value = false
  }
}

onMounted(() => {
  load()
  timer = window.setInterval(load, 15000)
})
onUnmounted(() => { if (timer) window.clearInterval(timer) })
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Security — SIP brute-force sentry</h2>
      <template v-if="metrics">
        <p v-if="!metrics.enabled" class="muted">
          The sentry is disabled on gofaxserver (<code>security.enabled=false</code>). Nothing is being tracked or banned.
        </p>
        <div v-else class="kv-grid">
          <div><div class="k">Active bans</div><div class="v"><strong>{{ metrics.active_bans }}</strong></div></div>
          <div><div class="k">Blocked hits</div><div class="v">{{ metrics.blocked_hits }}</div></div>
          <div><div class="k">Failures seen</div><div class="v">{{ metrics.failures_seen }}</div></div>
          <div><div class="k">Bans created</div><div class="v">{{ metrics.bans_created }}</div></div>
          <div><div class="k">Bans expired</div><div class="v">{{ metrics.bans_expired }}</div></div>
          <div><div class="k">Auto-unbans (endpoint)</div><div class="v">{{ metrics.reconcile_unbans }}</div></div>
          <div><div class="k">Firewall errors</div><div class="v" :class="{ error: metrics.enforce_errors > 0 }">{{ metrics.enforce_errors }}</div></div>
          <div><div class="k">Backend</div><div class="v">{{ metrics.backend }}</div></div>
          <div><div class="k">Threshold</div><div class="v">{{ metrics.fail_threshold }} / {{ metrics.fail_window }}</div></div>
        </div>
      </template>
      <p v-if="error" class="error">{{ error }}</p>
      <p v-if="okMsg" class="okmsg">{{ okMsg }}</p>
    </div>

    <div class="panel" v-if="metrics?.enabled">
      <h3>Manual ban</h3>
      <form class="inline" @submit.prevent="ban">
        <div><label>IP address</label><input v-model="banIP" placeholder="203.0.113.10" required /></div>
        <div><label>Reason (optional)</label><input v-model="banReason" placeholder="scanner" /></div>
        <div><label>Duration (optional, e.g. 24h; empty = permanent)</label><input v-model="banDuration" placeholder="24h" /></div>
        <button :disabled="banBusy">Ban</button>
      </form>
    </div>

    <div class="panel" v-if="metrics?.enabled">
      <form class="inline" @submit.prevent="load">
        <div><label><input type="checkbox" v-model="showHistory" @change="load" /> Include ban history</label></div>
      </form>
      <table>
        <thead>
          <tr>
            <th>IP</th><th>Status</th><th>Source</th><th>Offense #</th><th>Failures</th>
            <th>Banned at</th><th>Expires</th><th>Reason</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in bans" :key="b.id">
            <td><code>{{ b.ip }}</code></td>
            <td>
              <span v-if="b.active">banned</span>
              <span v-else class="muted">unbanned<span v-if="b.unban_reason"> ({{ b.unban_reason }})</span></span>
            </td>
            <td>{{ b.source }}</td>
            <td>{{ b.offense_count }}</td>
            <td>{{ b.fail_count }}</td>
            <td class="muted">{{ new Date(b.banned_at).toLocaleString() }}</td>
            <td class="muted">{{ expiry(b) }}</td>
            <td class="muted" style="max-width:280px;overflow-wrap:anywhere">{{ b.reason }}</td>
            <td>
              <button v-if="b.active" class="secondary" @click="unban(b.ip)">Unban</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="!bans.length" class="muted">No banned IPs.</p>
      <p class="muted" style="margin-top:0.5rem">
        Bans are enforced at the host firewall and lifted automatically when the IP is added as a
        gateway endpoint or matches a whitelist CIDR.
      </p>
    </div>
  </main>
</template>
