import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './auth'

const router = createRouter({
  history: createWebHistory('/portal/'),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
    // user realm
    { path: '/app', component: () => import('./views/user/SendFaxView.vue'), meta: { role: 'user' } },
    { path: '/app/faxes', component: () => import('./views/user/JobsView.vue'), meta: { role: 'user' } },
    { path: '/app/inbox', component: () => import('./views/user/InboxView.vue'), meta: { role: 'user' } },
    // admin realm
    { path: '/admin', component: () => import('./views/admin/OrgsView.vue'), meta: { role: 'admin' } },
    {
      path: '/admin/orgs/:id',
      component: () => import('./views/admin/OrgDetailView.vue'),
      meta: { role: 'admin' },
      children: [
        { path: '', redirect: (to) => `/admin/orgs/${to.params.id}/users` },
        {
          path: 'users',
          component: () => import('./components/admin/UserManager.vue'),
          props: (r) => ({ orgId: Number(r.params.id) }),
          meta: { role: 'admin' },
        },
        {
          path: 'numbers',
          component: () => import('./components/admin/NumberManager.vue'),
          props: (r) => ({ orgId: Number(r.params.id) }),
          meta: { role: 'admin' },
        },
        {
          path: 'settings',
          component: () => import('./components/admin/OrgSettings.vue'),
          props: (r) => ({ orgId: Number(r.params.id) }),
          meta: { role: 'admin' },
        },
      ],
    },
    // legacy flat routes, superseded by the org detail tabs
    { path: '/admin/numbers', redirect: '/admin' },
    { path: '/admin/users', redirect: '/admin' },
    { path: '/admin/tenants', component: () => import('./views/admin/TenantsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/tenants/:id', component: () => import('./views/admin/TenantDetailView.vue'), meta: { role: 'admin' } },
    { path: '/admin/endpoints', component: () => import('./views/admin/EndpointsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/gateways', component: () => import('./views/admin/GatewaysView.vue'), meta: { role: 'admin' } },
    { path: '/admin/templates', component: () => import('./views/admin/TemplatesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/dialplan', component: () => import('./views/admin/DialplanView.vue'), meta: { role: 'admin' } },
    { path: '/admin/fax-policies', component: () => import('./views/admin/FaxPoliciesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/active', component: () => import('./views/admin/ActiveFaxesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/jobs', component: () => import('./views/admin/JobsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/inbound', component: () => import('./views/admin/InboundView.vue'), meta: { role: 'admin' } },
    { path: '/admin/audit', component: () => import('./views/admin/AuditView.vue'), meta: { role: 'admin' } },
    { path: '/admin/branding', component: () => import('./views/admin/BrandingView.vue'), meta: { role: 'admin' } },
    // SPA root: the guard below sends users to their role home (or /login
    // when signed out). A static `redirect: '/login'` here would resolve
    // BEFORE the guard's fetchMe() completes, dumping signed-in users on
    // the login page. The component is a fallback only — the guard always
    // redirects this path first.
    { path: '/', component: () => import('./views/LoginView.vue') },
  ],
})

let meLoaded = false

function roleHome(auth: ReturnType<typeof useAuthStore>) {
  return auth.me?.role === 'admin' ? '/admin' : '/app'
}

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!meLoaded) {
    await auth.fetchMe()
    meLoaded = true
  }
  if (to.path === '/') {
    return auth.me ? roleHome(auth) : '/login'
  }
  if (to.path.startsWith('/login')) {
    // Already signed in: skip the login page.
    return auth.me ? roleHome(auth) : true
  }
  if (!auth.me) return '/login'
  if (to.meta.role && to.meta.role !== auth.me.role) {
    return roleHome(auth)
  }
  return true
})

export default router
