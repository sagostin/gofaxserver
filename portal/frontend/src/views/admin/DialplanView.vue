<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../../api'

interface Rule {
  id: number
  position: number
  pattern: string
  replacement: string
  enabled: boolean
  description: string
}

const source = ref('config')
const rules = ref<Rule[]>([])
const error = ref('')
const busy = ref(false)
const editing = ref<Rule | null>(null)
const isNew = ref(false)

const sorted = computed(() => [...rules.value].sort((a, b) => a.position - b.position))

async function load() {
  error.value = ''
  try {
    const d = await api<{ source: string; rules: Rule[] }>('/admin/dialplan')
    source.value = d.source
    rules.value = d.rules || []
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

function startNew() {
  isNew.value = true
  editing.value = { id: 0, position: 0, pattern: '', replacement: '', enabled: true, description: '' }
}

function startEdit(r: Rule) {
  isNew.value = false
  editing.value = { ...r }
}

async function save() {
  if (!editing.value) return
  error.value = ''
  busy.value = true
  try {
    if (isNew.value) {
      await api('/admin/dialplan/rules', { json: editing.value })
    } else {
      await api(`/admin/dialplan/rules/${editing.value.id}`, { method: 'PUT', json: editing.value })
    }
    editing.value = null
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function remove(r: Rule) {
  if (!confirm(`Delete rule ${r.pattern} → ${r.replacement}?`)) return
  error.value = ''
  try { await api(`/admin/dialplan/rules/${r.id}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}

async function toggle(r: Rule) {
  error.value = ''
  try {
    await api(`/admin/dialplan/rules/${r.id}`, { method: 'PUT', json: { ...r, enabled: !r.enabled } })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function move(r: Rule, dir: -1 | 1) {
  const list = sorted.value
  const idx = list.findIndex(x => x.id === r.id)
  const swap = list[idx + dir]
  if (!swap) return
  error.value = ''
  try {
    await api('/admin/dialplan/rules/reorder', {
      json: [
        { id: r.id, position: swap.position },
        { id: swap.id, position: r.position },
      ],
    })
    await load()
  } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Dialplan rules</h2>
      <p v-if="source === 'db'" class="muted">
        Source: <b>database</b> — rules below are live and apply in order to every caller/callee
        number before routing. Changes take effect immediately.
      </p>
      <p v-else class="muted">
        Source: <b>config</b> — rules below are stored but <b>inactive</b>. Set
        <code>dialplan.source = "db"</code> in gofaxserver's config.json (and restart) to manage
        the dialplan here with hot reload. Until then, rules come from the config file's
        <code>dialplan.rules</code> or the built-in defaults.
      </p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>#</th><th>Enabled</th><th>Pattern</th><th>Replacement</th><th>Description</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in sorted" :key="r.id">
            <td>
              <button class="secondary" :disabled="i === 0" @click="move(r, -1)">↑</button>
              <button class="secondary" :disabled="i === sorted.length - 1" @click="move(r, 1)">↓</button>
            </td>
            <td><input type="checkbox" :checked="r.enabled" @change="toggle(r)" /></td>
            <td><code>{{ r.pattern }}</code></td>
            <td><code>{{ r.replacement }}</code></td>
            <td>{{ r.description }}</td>
            <td class="actions-cell">
              <button class="secondary" @click="startEdit(r)">Edit</button>
              <button class="danger" @click="remove(r)">Delete</button>
            </td>
          </tr>
          <tr v-if="!sorted.length"><td colspan="6" class="muted">No rules yet.</td></tr>
        </tbody>
      </table>
      <button class="secondary" style="margin-top:8px" @click="startNew">New rule</button>
    </div>

    <div v-if="editing" class="panel">
      <h3>{{ isNew ? 'New rule' : `Edit rule #${editing.id}` }}</h3>
      <form class="inline" @submit.prevent="save">
        <div style="flex:2"><label>Pattern (regex)</label><input v-model="editing.pattern" placeholder="^1(\d{10}).*$" required style="width:100%" /></div>
        <div><label>Replacement</label><input v-model="editing.replacement" placeholder="$1" /></div>
        <div><label>Position</label><input v-model.number="editing.position" :disabled="isNew" /></div>
        <div style="flex:1"><label>Description</label><input v-model="editing.description" style="width:100%" /></div>
        <div style="align-self:center"><label style="font-size:13px;color:var(--text)"><input type="checkbox" v-model="editing.enabled" /> enabled</label></div>
        <button :disabled="busy">Save</button>
        <button type="button" class="secondary" @click="editing = null">Cancel</button>
      </form>
      <p class="muted">Replacement supports capture groups: <code>$1</code>, <code>$2</code>, …</p>
    </div>
  </main>
</template>
