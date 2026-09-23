<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../../api'

interface Rule {
  id: number
  scope: string
  src_number: string
  dst_number: string
  effect: string
  applies_to: string
  origin: string
  enabled: boolean
  expires_at: string | null
  failure_count: number
  success_count: number
  last_seen_at: string | null
  last_t38_status: string
  notes: string
  created_at: string
  updated_at: string
}

interface PairState {
  id: number
  src_number: string
  dst_number: string
  call_type: string
  last_used_t38: boolean
  last_seen: string
}

const rules = ref<Rule[]>([])
const pairStates = ref<PairState[]>([])
const error = ref('')
const busy = ref(false)
const editing = ref<Rule | null>(null)
const isNew = ref(false)
const filterNumber = ref('')
const filterOrigin = ref('')
const expiryHours = ref<number | null>(null)

// resolve dry-run
const resolveSrc = ref('')
const resolveDst = ref('')
const resolveType = ref('softmodem')
const resolveResult = ref<any>(null)

const scopes = ['dst', 'src', 'pair']
const effects = ['t38_off', 't38_on', 'ecm_off', 'ecm_on', 'v17_off', 'softmodem_only']
const appliesTo = ['both', 'softmodem', 'bridge']

const filtered = computed(() => rules.value)

async function load() {
  error.value = ''
  try {
    const qs = new URLSearchParams()
    if (filterNumber.value) qs.set('number', filterNumber.value)
    if (filterOrigin.value) qs.set('origin', filterOrigin.value)
    const d = await api<{ rules: Rule[] }>('/admin/fax-policies' + (qs.size ? '?' + qs : ''))
    rules.value = d.rules || []
  } catch (e: any) { error.value = e.message }
}

async function loadPairStates() {
  try {
    const d = await api<{ pair_states: PairState[] }>('/admin/fax-policies/pair-states')
    pairStates.value = d.pair_states || []
  } catch (e: any) { error.value = e.message }
}

onMounted(() => { load(); loadPairStates() })

function startNew() {
  isNew.value = true
  expiryHours.value = null
  editing.value = {
    id: 0, scope: 'dst', src_number: '', dst_number: '', effect: 't38_off',
    applies_to: 'both', origin: 'manual', enabled: true, expires_at: null,
    failure_count: 0, success_count: 0, last_seen_at: null, last_t38_status: '',
    notes: '', created_at: '', updated_at: '',
  }
}

function startEdit(r: Rule) {
  isNew.value = false
  expiryHours.value = null
  editing.value = { ...r }
}

async function save() {
  if (!editing.value) return
  error.value = ''
  busy.value = true
  try {
    const payload: any = { ...editing.value }
    if (expiryHours.value && expiryHours.value > 0) {
      payload.expires_at = new Date(Date.now() + expiryHours.value * 3600 * 1000).toISOString()
    }
    if (isNew.value) {
      await api('/admin/fax-policies', { json: payload })
    } else {
      await api(`/admin/fax-policies/${editing.value.id}`, { method: 'PUT', json: payload })
    }
    editing.value = null
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function remove(r: Rule) {
  if (!confirm(`Delete rule #${r.id} (${r.effect} ${r.scope === 'pair' ? r.src_number + '→' + r.dst_number : r.dst_number || r.src_number})?`)) return
  error.value = ''
  try { await api(`/admin/fax-policies/${r.id}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}

async function toggle(r: Rule) {
  error.value = ''
  try {
    await api(`/admin/fax-policies/${r.id}`, { method: 'PUT', json: { ...r, enabled: !r.enabled } })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function expire(r: Rule) {
  error.value = ''
  try { await api(`/admin/fax-policies/${r.id}/expire`, { method: 'POST' }); await load() }
  catch (e: any) { error.value = e.message }
}

async function removePairState(p: PairState) {
  error.value = ''
  try { await api(`/admin/fax-policies/pair-states/${p.id}`, { method: 'DELETE' }); await loadPairStates() }
  catch (e: any) { error.value = e.message }
}

async function runResolve() {
  error.value = ''
  resolveResult.value = null
  try {
    const qs = new URLSearchParams({ src: resolveSrc.value, dst: resolveDst.value, type: resolveType.value })
    resolveResult.value = await api('/admin/fax-policies/resolve?' + qs)
  } catch (e: any) { error.value = e.message }
}

function fmtTime(t: string | null) {
  if (!t) return '—'
  return new Date(t).toLocaleString()
}

function fmtExpiry(r: Rule) {
  if (!r.expires_at) return 'never'
  const d = new Date(r.expires_at)
  return d.getTime() < Date.now() ? 'expired' : d.toLocaleString()
}

function scopeLabel(r: Rule) {
  if (r.scope === 'pair') return `${r.src_number} → ${r.dst_number}`
  if (r.scope === 'dst') return r.dst_number
  return r.src_number
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Fax policies (T.38 / ECM / V.17)</h2>
      <p class="muted">
        Postgres-backed rules replacing the old FreeSWITCH mod_db softmodem fallback.
        Rules are composable: per attribute the most specific scope wins
        (<b>pair &gt; dst &gt; src</b>), <b>manual</b> beats <b>auto</b>, and at equal
        specificity "off" beats "on". <b>Auto</b> rules are learned from fax failures on the
        softmodem path (T.38 → ECM → V.17 escalation) and heal after consecutive successes
        or expiry — bridged (transcoded) calls are probed via flip-flop when no rule decides.
        Changes take effect immediately.
      </p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <form class="inline" @submit.prevent="load">
        <div><label>Filter number</label><input v-model="filterNumber" placeholder="2505551234" /></div>
        <div>
          <label>Origin</label>
          <select v-model="filterOrigin">
            <option value="">all</option>
            <option value="manual">manual</option>
            <option value="auto">auto</option>
          </select>
        </div>
        <button class="secondary" type="submit">Filter</button>
        <button class="secondary" type="button" @click="startNew">New rule</button>
      </form>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr>
            <th>ID</th><th>Enabled</th><th>Scope</th><th>Number(s)</th><th>Effect</th>
            <th>Applies to</th><th>Origin</th><th>F/S</th><th>Expires</th><th>Notes</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in filtered" :key="r.id">
            <td>{{ r.id }}</td>
            <td><input type="checkbox" :checked="r.enabled" @change="toggle(r)" /></td>
            <td>{{ r.scope }}</td>
            <td><code>{{ scopeLabel(r) }}</code></td>
            <td><code>{{ r.effect }}</code></td>
            <td>{{ r.applies_to }}</td>
            <td>{{ r.origin }}</td>
            <td>{{ r.failure_count }}/{{ r.success_count }}</td>
            <td>{{ fmtExpiry(r) }}</td>
            <td>{{ r.notes }}</td>
            <td class="actions-cell">
              <button class="secondary" @click="startEdit(r)">Edit</button>
              <button class="secondary" title="Expire now (reset to probing)" @click="expire(r)">Expire</button>
              <button class="danger" @click="remove(r)">Delete</button>
            </td>
          </tr>
          <tr v-if="!filtered.length"><td colspan="11" class="muted">No rules yet.</td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="editing" class="panel">
      <h3>{{ isNew ? 'New rule' : `Edit rule #${editing.id}${editing.origin === 'auto' ? ' (auto-learned)' : ''}` }}</h3>
      <form class="inline" @submit.prevent="save">
        <div>
          <label>Scope</label>
          <select v-model="editing.scope">
            <option v-for="s in scopes" :key="s" :value="s">{{ s }}</option>
          </select>
        </div>
        <div v-if="editing.scope !== 'dst'"><label>Source number</label><input v-model="editing.src_number" placeholder="2505550001" /></div>
        <div v-if="editing.scope !== 'src'"><label>Destination number</label><input v-model="editing.dst_number" placeholder="2505550002" /></div>
        <div>
          <label>Effect</label>
          <select v-model="editing.effect">
            <option v-for="e in effects" :key="e" :value="e">{{ e }}</option>
          </select>
        </div>
        <div>
          <label>Applies to</label>
          <select v-model="editing.applies_to">
            <option v-for="a in appliesTo" :key="a" :value="a">{{ a }}</option>
          </select>
        </div>
        <div><label>Expire after (hours, optional)</label><input type="number" min="0" v-model.number="expiryHours" placeholder="never" /></div>
        <div style="flex:1"><label>Notes</label><input v-model="editing.notes" style="width:100%" /></div>
        <div style="align-self:center"><label style="font-size:13px;color:var(--text)"><input type="checkbox" v-model="editing.enabled" /> enabled</label></div>
        <button :disabled="busy">Save</button>
        <button type="button" class="secondary" @click="editing = null">Cancel</button>
      </form>
      <p class="muted">
        <code>t38_off</code> = never request/offer T.38 for matching calls (softmodem fallback);
        <code>softmodem_only</code> = hard refusal of T.38 including far-end re-INVITEs;
        <code>applies_to</code> selects softmodem (txfax/rxfax) vs bridged (transcoded) calls.
      </p>
    </div>

    <div class="panel">
      <h3>Resolve (dry run)</h3>
      <form class="inline" @submit.prevent="runResolve">
        <div><label>Source</label><input v-model="resolveSrc" placeholder="2505550001" /></div>
        <div><label>Destination</label><input v-model="resolveDst" placeholder="2505550002" /></div>
        <div>
          <label>Call type</label>
          <select v-model="resolveType">
            <option value="softmodem">softmodem</option>
            <option value="bridge">bridge</option>
          </select>
        </div>
        <button class="secondary" type="submit">Resolve</button>
      </form>
      <div v-if="resolveResult" style="margin-top:8px">
        <p>
          enable_t38=<b>{{ resolveResult.policy.enable_t38 }}</b>,
          request_t38=<b>{{ resolveResult.policy.request_t38 }}</b>,
          decided_by_rule=<b>{{ resolveResult.policy.t38_decided }}</b>,
          ecm=<b>{{ resolveResult.policy.use_ecm === null ? 'inherit' : resolveResult.policy.use_ecm }}</b>,
          disable_v17=<b>{{ resolveResult.policy.disable_v17 === null ? 'inherit' : resolveResult.policy.disable_v17 }}</b>
        </p>
        <p v-if="resolveResult.applied_rules?.length" class="muted">
          Applied rules:
          <span v-for="r in resolveResult.applied_rules" :key="r.id">
            #{{ r.id }} ({{ r.scope }} {{ scopeLabel(r) }} {{ r.effect }}/{{ r.applies_to }} {{ r.origin }})
          </span>
        </p>
        <p v-else class="muted">No rules matched — config defaults / flip-flop probing apply.</p>
      </div>
    </div>

    <div class="panel">
      <h3>Flip-flop pair state (probing memory)</h3>
      <p class="muted">Persisted per src→dst pair and call type; used only when no rule decides T.38.</p>
      <table>
        <thead>
          <tr><th>ID</th><th>Pair</th><th>Call type</th><th>Last used T.38</th><th>Last seen</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="p in pairStates" :key="p.id">
            <td>{{ p.id }}</td>
            <td><code>{{ p.src_number }} → {{ p.dst_number }}</code></td>
            <td>{{ p.call_type }}</td>
            <td>{{ p.last_used_t38 }}</td>
            <td>{{ fmtTime(p.last_seen) }}</td>
            <td class="actions-cell"><button class="danger" @click="removePairState(p)">Clear</button></td>
          </tr>
          <tr v-if="!pairStates.length"><td colspan="6" class="muted">No pair state recorded yet.</td></tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
