<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Tpl {
  id: number
  name: string
  description: string
  body: string
  variables: string[]
}

const templates = ref<Tpl[]>([])
const error = ref('')
const busy = ref(false)
const editing = ref<Tpl | null>(null)
const isNew = ref(false)

async function load() {
  error.value = ''
  try { templates.value = (await api<Tpl[]>('/admin/gateway-templates')) || [] }
  catch (e: any) { error.value = e.message }
}
onMounted(load)

function startNew() {
  isNew.value = true
  editing.value = { id: 0, name: '', description: '', body: '<include>\n  <gateway name="{{.name}}">\n    <param name="realm" value="{{.realm}}"/>\n  </gateway>\n</include>\n', variables: [] }
}

function startEdit(t: Tpl) {
  isNew.value = false
  editing.value = { ...t }
}

async function save() {
  if (!editing.value) return
  error.value = ''
  busy.value = true
  try {
    if (isNew.value) {
      await api('/admin/gateway-templates', { json: editing.value })
    } else {
      await api(`/admin/gateway-templates/${editing.value.id}`, { method: 'PUT', json: editing.value })
    }
    editing.value = null
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function remove(t: Tpl) {
  if (!confirm(`Delete template ${t.name}? (Blocked while gateways still use it.)`)) return
  error.value = ''
  try { await api(`/admin/gateway-templates/${t.id}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Gateway templates</h2>
      <p class="muted">
        FreeSWITCH gateway XML templates, rendered with Go template syntax
        (e.g. <code v-text="'{{.realm}}'"></code> or conditionals like
        <code v-text="'{{if .register}}...{{end}}'"></code>).
        Declared variables drive the provision form on the Gateways tab.
      </p>
      <table>
        <thead>
          <tr><th>Name</th><th>Description</th><th>Variables</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="t in templates" :key="t.id">
            <td>{{ t.name }}</td>
            <td>{{ t.description }}</td>
            <td><code>{{ t.variables?.join(', ') }}</code></td>
            <td class="actions-cell">
              <button class="secondary" @click="startEdit(t)">Edit</button>
              <button class="danger" @click="remove(t)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
      <button class="secondary" style="margin-top:8px" @click="startNew">New template</button>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div v-if="editing" class="panel">
      <h3>{{ isNew ? 'New template' : `Edit ${editing.name}` }}</h3>
      <form @submit.prevent="save">
        <div class="inline">
          <div><label>Name</label><input v-model="editing.name" required :disabled="!isNew" /></div>
          <div style="flex:1"><label>Description</label><input v-model="editing.description" style="width:100%" /></div>
        </div>
        <label>Body (XML template)</label>
        <textarea v-model="editing.body" rows="14" spellcheck="false"
          style="width:100%;font-family:monospace;font-size:13px"></textarea>
        <div style="margin-top:8px">
          <button :disabled="busy">Save</button>
          <button type="button" class="secondary" style="margin-left:8px" @click="editing = null">Cancel</button>
        </div>
      </form>
    </div>
  </main>
</template>
