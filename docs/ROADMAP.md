# Roadmap

Planned / proposed work items. Newest first.

## Reconcile "adopt" action for out-of-band notify rules

**Status:** proposed — not implemented.

Numbers/tenants whose notify rules were edited out-of-band (direct on
gofaxserver, or via the direct admin UI) show as permanent `notify` drift in
the org reconcile report, and the next portal re-sync would silently delete
the unrecognized segments. The manual workaround is copying the extra
segments into the portal-managed `CustomNotify` / `TenantNotify` fields.

### Proposed design

`POST /admin/orgs/{id}/notify/adopt`: for each drifted number, compute
`live segments − computed segments`, append the extras into
`Number.CustomNotify` (tenant-level extras into `Org.TenantNotify`), and
re-push. One click converts out-of-band rules into portal-managed ones and
turns the reconcile report green. Add an "Adopt" button per drift row in
OrgsView.

## Per-org email notification templates (portal admin)

**Status:** proposed — not implemented.

Today the fax notification emails (`email_report`, `email_full`,
`email_full_failure`) are rendered by gofaxserver
(`gofaxserver/notify.go: emailSubjectBody`) with a fixed subject/body layout.
Operators want to customize the format per organization from the portal's
admin section (branding, wording, locale, which fields are shown).

### Proposed design

1. **Portal DB:** per-org `email_template` record (subject template, body
   template, enabled flag) with a sensible built-in default matching the
   current layout.
2. **Template variables:** `{{.Status}}`, `{{.Direction}}`, `{{.Caller}}`,
   `{{.CallerName}}`, `{{.Callee}}`, `{{.Pages}}`, `{{.Attempts}}`,
   `{{.ResultText}}`, `{{.HangupCause}}`, `{{.StartTs}}`, `{{.EndTs}}`,
   `{{.JobUUID}}` — rendered with Go `text/template` (same data as
   `PortalStatusPayload` plus direction/caller-name).
3. **Delivery options (pick one):**
   - **(a) Templates pushed to gofaxserver (recommended).** Extend the admin
     API with a per-tenant/number `email_template` field; the portal pushes
     template updates alongside the derived `notify` string (same resync
     paths as `computeNotify`). gofaxserver renders at dispatch time. Keeps a
     single email send path for both portal and non-portal flows.
   - **(b) Portal sends its own receipt emails.** Requires SMTP config in
     the portal and only covers portal-managed numbers; gofaxserver would
     still send for everything else. Simpler to build, but two send paths to
     keep consistent.
4. **Portal UI:** org settings page with subject/body editors, variable
   reference, live preview rendered from a recent job, and a "send test
   email" action.

### Open questions

- Per-number template overrides, or org-level only?
- HTML emails (multipart/alternative) vs plain text?
- Localization of direction/status strings?
