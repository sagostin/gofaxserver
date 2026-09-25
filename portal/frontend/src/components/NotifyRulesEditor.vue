<script setup lang="ts">
import { computed, ref, watch } from 'vue'

// Edits a gofaxserver notify string ("type->destination,type->destination")
// as a list of structured rules. v-model is the serialized string, so the
// parent always has an API-ready value.
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

interface Rule {
  type: string
  dest: string
  raw?: boolean // segment we couldn't parse; kept verbatim until removed
}

const TYPES = [
  { value: 'email_report', hint: 'Delivery report email (PDF report attached)', placeholder: 'ops@acme.tld' },
  { value: 'email_full', hint: 'Full fax PDF by email', placeholder: 'ops@acme.tld' },
  { value: 'email_full_failure', hint: 'Full fax PDF by email on failures', placeholder: 'ops@acme.tld' },
  { value: 'webhook', hint: 'JSON webhook POST', placeholder: 'https://example.com/hook' },
  { value: 'webhook_form', hint: 'Form-encoded webhook POST', placeholder: 'https://example.com/hook' },
  { value: 'portal', hint: 'Push status to a gofaxserver user inbox', placeholder: 'username' },
  { value: 'gateway', hint: 'Route via a configured gateway', placeholder: 'gateway name' },
]

function parse(v: string): Rule[] {
  const out: Rule[] = []
  for (const seg of v.split(',')) {
    const s = seg.trim()
    if (!s) continue
    const i = s.indexOf('->')
    const type = i > 0 ? s.slice(0, i).trim() : ''
    const dest = i > 0 ? s.slice(i + 2).trim() : ''
    if (!type || !dest) {
      out.push({ type: '', dest: s, raw: true })
      continue
    }
    // Legacy alias accepted by gofaxserver; normalize to the canonical type.
    out.push({ type: type === 'email' ? 'email_report' : type, dest })
  }
  return out
}

function serialize(rs: Rule[]): string {
  return rs.map((r) => (r.raw ? r.dest : `${r.type}->${r.dest}`)).join(',')
}

const rules = ref<Rule[]>(parse(props.modelValue))
watch(
  () => props.modelValue,
  (v) => {
    // Only re-parse on external changes; ignore echoes of our own emits.
    if (v !== serialize(rules.value)) rules.value = parse(v)
  },
)

function sync() {
  emit('update:modelValue', serialize(rules.value))
}

const newType = ref('email_report')
const newDest = ref('')
const addError = ref('')
const activeType = computed(() => TYPES.find((t) => t.value === newType.value))

function addRule() {
  addError.value = ''
  const dest = newDest.value.trim()
  if (!dest) {
    addError.value = 'Enter a destination.'
    return
  }
  if (dest.includes(',') || dest.includes('->')) {
    addError.value = 'Destination cannot contain commas or "->" (separate multiple addresses with ";").'
    return
  }
  if (rules.value.some((r) => !r.raw && r.type === newType.value && r.dest === dest)) {
    addError.value = 'That rule already exists.'
    return
  }
  rules.value.push({ type: newType.value, dest })
  newDest.value = ''
  sync()
}

function removeRule(i: number) {
  rules.value.splice(i, 1)
  sync()
}
</script>

<template>
  <div class="notify-editor">
    <div v-if="rules.length" class="notify-rules">
      <div v-for="(r, i) in rules" :key="i" class="notify-rule">
        <template v-if="r.raw">
          <span class="type raw">unparsed</span>
          <span class="dest" title="Not valid type->destination syntax; kept as-is. Remove it or fix it upstream.">{{ r.dest }}</span>
        </template>
        <template v-else>
          <span class="type">{{ r.type }}</span>
          <span class="dest">{{ r.dest }}</span>
        </template>
        <button type="button" class="secondary" title="Remove rule" @click="removeRule(i)">✕</button>
      </div>
    </div>
    <p v-else class="muted" style="margin:4px 0">No rules yet.</p>

    <div class="notify-add">
      <div>
        <label>Type</label>
        <select v-model="newType">
          <option v-for="t in TYPES" :key="t.value" :value="t.value">{{ t.value }}</option>
        </select>
      </div>
      <div style="flex:1; min-width:200px">
        <label>Destination</label>
        <input
          v-model="newDest"
          :placeholder="activeType?.placeholder"
          @keydown.enter.prevent="addRule"
        />
      </div>
      <button type="button" class="secondary" @click="addRule">Add rule</button>
    </div>
    <p v-if="activeType" class="muted" style="margin:4px 0 0">{{ activeType.hint }}</p>
    <p v-if="addError" class="error">{{ addError }}</p>
  </div>
</template>
