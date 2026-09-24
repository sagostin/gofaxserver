import { ref } from 'vue'
import { api } from './api'

// Shared scope-target pickers for admin forms (endpoints, gateways).
// A scope target is either a portal-managed org/number (type_id = portal id,
// translated server-side via scope_source=portal) or a direct upstream
// gofaxserver tenant/number (type_id = gofax id, scope_source=direct).

export interface ScopeOption {
  id: number
  label: string
}

interface PortalOrg { id: number; name: string; gofax_tenant_id: number }
interface PortalNumber { id: number; number: string; org_name: string }
interface UpstreamTenant { id: number; name: string }
interface UpstreamNumber { id: number; tenant_id: number; number: string; name: string }

export function useScopeTargets() {
  const orgs = ref<PortalOrg[]>([])
  const portalNumbers = ref<PortalNumber[]>([])
  const tenants = ref<UpstreamTenant[]>([])
  const upstreamNumbers = ref<UpstreamNumber[]>([])
  const loadError = ref('')

  async function load() {
    loadError.value = ''
    const [o, n, t, un] = await Promise.all([
      api<PortalOrg[]>('/admin/orgs').catch(() => []),
      api<PortalNumber[]>('/admin/numbers').catch(() => []),
      api<UpstreamTenant[]>('/admin/upstream/tenants').catch(() => []),
      api<UpstreamNumber[]>('/admin/upstream/numbers').catch(() => []),
    ])
    orgs.value = o || []
    portalNumbers.value = n || []
    tenants.value = t || []
    upstreamNumbers.value = un || []
  }

  function options(type: string, source: string): ScopeOption[] {
    if (type === 'tenant' && source === 'portal') {
      return orgs.value.map(o => ({ id: o.id, label: `${o.name} (org → tenant #${o.gofax_tenant_id})` }))
    }
    if (type === 'number' && source === 'portal') {
      return portalNumbers.value.map(n => ({ id: n.id, label: `${n.number} — ${n.org_name}` }))
    }
    if (type === 'tenant') {
      return tenants.value.map(t => ({ id: t.id, label: `${t.name} (#${t.id})` }))
    }
    if (type === 'number') {
      return upstreamNumbers.value.map(n => ({ id: n.id, label: `${n.number}${n.name ? ' — ' + n.name : ''} (tenant #${n.tenant_id})` }))
    }
    return []
  }

  return { orgs, portalNumbers, tenants, upstreamNumbers, loadError, load, options }
}
