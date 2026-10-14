// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

// ipsentry.go implements the SIP brute-force sentry: failed-ACL inbound
// attempts are tracked per source IP; repeat offenders are banned at the
// host firewall (nftables/ipset) for escalating durations. Bans persist in
// Postgres and are auto-revoked when the IP later matches a gateway ACL
// (e.g. it becomes a customer endpoint) or a whitelist CIDR.

package gofaxserver

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

// BlockedIP is a persisted ban record. ExpiresAt nil = permanent ban.
// Historical (inactive) rows are kept for audit/metrics.
type BlockedIP struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	IP           string     `gorm:"uniqueIndex;size:45" json:"ip"`
	Reason       string     `json:"reason"`
	Source       string     `json:"source"` // "auto" (sentry) or "manual" (admin)
	FailCount    int        `json:"fail_count"`
	OffenseCount int        `json:"offense_count"`
	BannedAt     time.Time  `json:"banned_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"` // nil = permanent
	Active       bool       `gorm:"index" json:"active"`
	UnbannedAt   *time.Time `json:"unbanned_at,omitempty"`
	UnbanReason  string     `json:"unban_reason,omitempty"`
}

// blockedLogSampleInterval bounds how often a hit from an already-banned IP
// is logged (the kernel drops the packets anyway; this covers log-only mode
// and the propagation gap).
const blockedLogSampleInterval = 5 * time.Minute

// IPSentry tracks failed-ACL attempts and manages dynamic IP bans.
type IPSentry struct {
	server  *Server
	cfg     gofaxlib.SecurityConfig
	backend sentryBackend

	mu            sync.Mutex
	failures      map[string][]time.Time // sliding window of failed-ACL hits
	banned        map[string]*BlockedIP  // active bans (fast-path cache)
	lastLog       map[string]time.Time   // blocked-hit log sampling
	offenses      map[string]int         // lifetime ban count per IP (DB is authoritative when present)
	whitelistNets []*net.IPNet
	whitelistIPs  map[string]bool

	failuresSeen     atomic.Int64
	blockedHits      atomic.Int64
	bansCreated      atomic.Int64
	bansExpired      atomic.Int64
	reconcileUnbans  atomic.Int64
	manualBans       atomic.Int64
	manualUnbans     atomic.Int64
	enforceErrors    atomic.Int64
	whitelistSkipped atomic.Int64

	stopCh  chan struct{}
	stopped sync.WaitGroup
}

// newIPSentry builds the sentry from config, bootstraps the firewall
// backend, and re-applies persisted active bans. Returns nil when the
// sentry is disabled in config.
func newIPSentry(s *Server) (*IPSentry, error) {
	cfg := gofaxlib.Config.Security
	if !cfg.Enabled {
		return nil, nil
	}

	ip := &IPSentry{
		server:       s,
		cfg:          cfg,
		failures:     make(map[string][]time.Time),
		banned:       make(map[string]*BlockedIP),
		lastLog:      make(map[string]time.Time),
		offenses:     make(map[string]int),
		whitelistIPs: make(map[string]bool),
		stopCh:       make(chan struct{}),
	}

	for _, cidr := range cfg.WhitelistCIDRs {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			ip.whitelistNets = append(ip.whitelistNets, n)
			continue
		}
		if parsed := net.ParseIP(cidr); parsed != nil {
			ip.whitelistIPs[parsed.String()] = true
			continue
		}
		ip.log(logrus.WarnLevel, fmt.Sprintf("ignoring invalid whitelist_cidrs entry %q", cidr), nil)
	}

	backend, err := newSentryBackend(cfg.BackendName(), ip)
	if err != nil {
		return nil, fmt.Errorf("security backend %q: %w", cfg.BackendName(), err)
	}
	ip.backend = backend

	if err := backend.Bootstrap(); err != nil {
		// Non-fatal: detection and log suppression still work; bans are
		// recorded in the DB and retried by the janitor.
		ip.enforceErrors.Add(1)
		ip.log(logrus.ErrorLevel, fmt.Sprintf("firewall bootstrap failed (bans will not be enforced until fixed): %v", err), nil)
	}

	// Re-apply persisted active bans (restart survival).
	var rows []BlockedIP
	if s.DB != nil {
		if err := s.DB.Where("active = ?", true).Find(&rows).Error; err != nil {
			ip.log(logrus.ErrorLevel, fmt.Sprintf("failed to load active bans: %v", err), nil)
		}
	}
	now := time.Now()
	for i := range rows {
		row := rows[i]
		if row.ExpiresAt != nil && row.ExpiresAt.Before(now) {
			ip.unbanIP(row.IP, "expired-during-downtime")
			continue
		}
		if err := ip.backend.Ban(row.IP, row.ExpiresAt); err != nil {
			ip.enforceErrors.Add(1)
			ip.log(logrus.ErrorLevel, fmt.Sprintf("failed to re-apply ban for %s: %v", row.IP, err), map[string]interface{}{"ip": row.IP})
		}
		rowCopy := row
		ip.banned[row.IP] = &rowCopy
	}
	if len(rows) > 0 {
		ip.log(logrus.InfoLevel, fmt.Sprintf("re-applied %d persisted ban(s)", len(ip.banned)), map[string]interface{}{"count": len(ip.banned)})
	}

	return ip, nil
}

// Start launches the expiry/reconcile janitor goroutine.
func (ip *IPSentry) Start() {
	ip.stopped.Add(1)
	go func() {
		defer ip.stopped.Done()
		ticker := time.NewTicker(ip.cfg.JanitorInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ip.stopCh:
				return
			case <-ticker.C:
				ip.janitorTick()
			}
		}
	}()
	ip.log(logrus.InfoLevel, fmt.Sprintf("IP sentry started (backend=%s, threshold=%d/%s)", ip.backend.Name(), ip.cfg.Threshold(), ip.cfg.Window()), nil)
}

// Stop halts the janitor.
func (ip *IPSentry) Stop() {
	close(ip.stopCh)
	ip.stopped.Wait()
}

func (ip *IPSentry) log(level logrus.Level, msg string, fields map[string]interface{}) {
	if ip.server != nil && ip.server.LogManager != nil {
		ip.server.LogManager.SendLog(ip.server.LogManager.BuildLog("Security.Sentry", msg, level, fields))
		return
	}
	logrus.WithFields(logrus.Fields{"type": "SECURITY.SENTRY"}).Log(level, msg)
}

// IsWhitelisted reports whether the IP can never be banned.
func (ip *IPSentry) IsWhitelisted(ipStr string) bool {
	parsed := net.ParseIP(ipStr)
	if parsed == nil {
		return false
	}
	ip.mu.Lock()
	defer ip.mu.Unlock()
	if ip.whitelistIPs[parsed.String()] {
		return true
	}
	for _, n := range ip.whitelistNets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// IsBanned reports whether the IP currently has an active ban. Fast path:
// in-memory cache only, no DB or firewall access.
func (ip *IPSentry) IsBanned(ipStr string) bool {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	b, ok := ip.banned[ipStr]
	if !ok {
		return false
	}
	if b.ExpiresAt != nil && b.ExpiresAt.Before(time.Now()) {
		// Lazily treat as unbanned; the janitor performs the actual cleanup.
		return false
	}
	return true
}

// RecordFailure records a failed-ACL attempt from the given source IP and
// bans it when the configured threshold/window is exceeded.
func (ip *IPSentry) RecordFailure(ipStr string) {
	if net.ParseIP(ipStr) == nil {
		return
	}
	ip.failuresSeen.Add(1)

	if ip.IsWhitelisted(ipStr) {
		ip.whitelistSkipped.Add(1)
		return
	}
	if ip.IsBanned(ipStr) {
		return
	}

	now := time.Now()
	window := ip.cfg.Window()

	ip.mu.Lock()
	kept := ip.failures[ipStr][:0]
	for _, t := range ip.failures[ipStr] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	ip.failures[ipStr] = kept
	count := len(kept)
	ip.mu.Unlock()

	if count >= ip.cfg.Threshold() {
		ip.banIP(ipStr, "auto",
			fmt.Sprintf("%d failed ACL attempts within %s", count, window), count)
	}
}

// banIP bans an IP with escalating duration based on prior offenses.
func (ip *IPSentry) banIP(ipStr, source, reason string, failCount int) {
	// Never ban an address that matches a gateway ACL — it is (or just
	// became) a legitimate endpoint.
	if ip.server != nil {
		if _, err := ip.server.fsGatewayACL(ipStr); err == nil {
			ip.whitelistSkipped.Add(1)
			ip.log(logrus.WarnLevel, fmt.Sprintf("refusing to ban %s: matches a gateway endpoint ACL", ipStr), map[string]interface{}{"ip": ipStr})
			return
		}
	}

	now := time.Now()

	ip.mu.Lock()
	existing, already := ip.banned[ipStr]
	ip.mu.Unlock()
	if already && existing.Active {
		return
	}

	offense := 1
	ip.mu.Lock()
	offense = ip.offenses[ipStr] + 1
	ip.mu.Unlock()
	if ip.server != nil && ip.server.DB != nil {
		var prev BlockedIP
		if err := ip.server.DB.Where("ip = ?", ipStr).First(&prev).Error; err == nil {
			if prev.OffenseCount+1 > offense {
				offense = prev.OffenseCount + 1
			}
			if prev.FailCount > failCount {
				failCount = prev.FailCount
			}
		}
	}
	ip.mu.Lock()
	ip.offenses[ipStr] = offense
	ip.mu.Unlock()

	durations := ip.cfg.Durations()
	var expires *time.Time
	if offense <= len(durations) {
		t := now.Add(durations[offense-1])
		expires = &t
	} // else: permanent

	rec := BlockedIP{
		IP:           ipStr,
		Reason:       reason,
		Source:       source,
		FailCount:    failCount,
		OffenseCount: offense,
		BannedAt:     now,
		ExpiresAt:    expires,
		Active:       true,
	}

	if err := ip.backend.Ban(ipStr, expires); err != nil {
		ip.enforceErrors.Add(1)
		ip.log(logrus.ErrorLevel, fmt.Sprintf("firewall ban of %s failed (recorded in DB, janitor will retry): %v", ipStr, err), map[string]interface{}{"ip": ipStr})
	}

	if ip.server != nil && ip.server.DB != nil {
		var prev BlockedIP
		if err := ip.server.DB.Where("ip = ?", ipStr).First(&prev).Error; err == nil {
			rec.ID = prev.ID
			if err := ip.server.DB.Save(&rec).Error; err != nil {
				ip.log(logrus.ErrorLevel, fmt.Sprintf("failed to persist ban for %s: %v", ipStr, err), nil)
			}
		} else if err := ip.server.DB.Create(&rec).Error; err != nil {
			ip.log(logrus.ErrorLevel, fmt.Sprintf("failed to persist ban for %s: %v", ipStr, err), nil)
		}
	}

	ip.mu.Lock()
	recCopy := rec
	ip.banned[ipStr] = &recCopy
	delete(ip.failures, ipStr)
	ip.mu.Unlock()

	ip.bansCreated.Add(1)
	if source == "manual" {
		ip.manualBans.Add(1)
	}

	dur := "permanent"
	if expires != nil {
		dur = time.Until(*expires).Round(time.Second).String()
	}
	ip.log(logrus.WarnLevel, fmt.Sprintf("banned %s (offense #%d, duration %s): %s", ipStr, offense, dur, reason),
		map[string]interface{}{"ip": ipStr, "offense": offense, "duration": dur, "source": source})
}

// BanManual imposes a ban from the admin API. Nil duration = permanent.
func (ip *IPSentry) BanManual(ipStr, reason string, duration *time.Duration) error {
	if net.ParseIP(ipStr) == nil {
		return fmt.Errorf("invalid IP address %q", ipStr)
	}
	if ip.IsWhitelisted(ipStr) {
		return fmt.Errorf("%s is whitelisted and cannot be banned", ipStr)
	}
	if ip.IsBanned(ipStr) {
		return fmt.Errorf("%s is already banned", ipStr)
	}
	if duration == nil {
		// Permanent manual ban: set offense beyond the duration ladder.
		ip.banIPPermanent(ipStr, reason)
		return nil
	}
	now := time.Now()
	exp := now.Add(*duration)
	rec := BlockedIP{IP: ipStr, Reason: reason, Source: "manual", OffenseCount: 1, BannedAt: now, ExpiresAt: &exp, Active: true}
	if ip.server != nil && ip.server.DB != nil {
		var prev BlockedIP
		if err := ip.server.DB.Where("ip = ?", ipStr).First(&prev).Error; err == nil {
			rec.ID = prev.ID
			rec.OffenseCount = prev.OffenseCount + 1
			rec.FailCount = prev.FailCount
			ip.server.DB.Save(&rec)
		} else {
			ip.server.DB.Create(&rec)
		}
	}
	if err := ip.backend.Ban(ipStr, &exp); err != nil {
		ip.enforceErrors.Add(1)
		return fmt.Errorf("firewall ban failed: %w", err)
	}
	ip.mu.Lock()
	recCopy := rec
	ip.banned[ipStr] = &recCopy
	ip.mu.Unlock()
	ip.bansCreated.Add(1)
	ip.manualBans.Add(1)
	ip.log(logrus.WarnLevel, fmt.Sprintf("manually banned %s until %s: %s", ipStr, exp.Format(time.RFC3339), reason),
		map[string]interface{}{"ip": ipStr, "source": "manual"})
	return nil
}

// banIPPermanent bans without expiry, regardless of offense ladder.
func (ip *IPSentry) banIPPermanent(ipStr, reason string) {
	offense := len(ip.cfg.Durations()) + 1 // past the ladder = permanent
	rec := BlockedIP{IP: ipStr, Reason: reason, Source: "manual", OffenseCount: offense, BannedAt: time.Now(), Active: true}
	if ip.server != nil && ip.server.DB != nil {
		var prev BlockedIP
		if err := ip.server.DB.Where("ip = ?", ipStr).First(&prev).Error; err == nil {
			rec.ID = prev.ID
			rec.FailCount = prev.FailCount
			ip.server.DB.Save(&rec)
		} else {
			ip.server.DB.Create(&rec)
		}
	}
	if err := ip.backend.Ban(ipStr, nil); err != nil {
		ip.enforceErrors.Add(1)
		ip.log(logrus.ErrorLevel, fmt.Sprintf("firewall ban of %s failed (recorded in DB): %v", ipStr, err), nil)
	}
	ip.mu.Lock()
	recCopy := rec
	ip.banned[ipStr] = &recCopy
	ip.mu.Unlock()
	ip.bansCreated.Add(1)
	ip.manualBans.Add(1)
	ip.log(logrus.WarnLevel, fmt.Sprintf("manually banned %s permanently: %s", ipStr, reason),
		map[string]interface{}{"ip": ipStr, "source": "manual"})
}

// UnbanIP revokes a ban (admin API). Returns false when the IP was not banned.
func (ip *IPSentry) UnbanIP(ipStr string) bool {
	if !ip.IsBanned(ipStr) {
		return false
	}
	ip.manualUnbans.Add(1)
	ip.unbanIP(ipStr, "manual")
	return true
}

// unbanIP is the internal unban path with an audit reason.
func (ip *IPSentry) unbanIP(ipStr, reason string) {
	if err := ip.backend.Unban(ipStr); err != nil {
		ip.enforceErrors.Add(1)
		ip.log(logrus.ErrorLevel, fmt.Sprintf("firewall unban of %s failed: %v", ipStr, err), map[string]interface{}{"ip": ipStr})
	}

	now := time.Now()
	ip.mu.Lock()
	delete(ip.banned, ipStr)
	delete(ip.failures, ipStr)
	ip.mu.Unlock()

	if ip.server != nil && ip.server.DB != nil {
		ip.server.DB.Model(&BlockedIP{}).Where("ip = ? AND active = ?", ipStr, true).
			Updates(map[string]interface{}{"active": false, "unbanned_at": now, "unban_reason": reason})
	}

	ip.log(logrus.InfoLevel, fmt.Sprintf("unbanned %s (%s)", ipStr, reason), map[string]interface{}{"ip": ipStr, "reason": reason})
}

// Reconcile releases bans whose IP now matches a gateway endpoint ACL or a
// whitelist entry — e.g. the IP was added as a customer endpoint after being
// banned, or a hostname-valued ACL entry re-resolved to it.
func (ip *IPSentry) Reconcile() {
	ip.mu.Lock()
	ips := make([]string, 0, len(ip.banned))
	for k := range ip.banned {
		ips = append(ips, k)
	}
	ip.mu.Unlock()

	for _, ipStr := range ips {
		if ip.IsWhitelisted(ipStr) {
			ip.reconcileUnbans.Add(1)
			ip.unbanIP(ipStr, "whitelisted")
			continue
		}
		if ip.server != nil {
			if _, err := ip.server.fsGatewayACL(ipStr); err == nil {
				ip.reconcileUnbans.Add(1)
				ip.unbanIP(ipStr, "now-matches-endpoint-acl")
			}
		}
	}
}

// janitorTick expires timed-out bans, retries enforcement of bans whose
// firewall rule is missing, and reconciles against the endpoint ACLs.
func (ip *IPSentry) janitorTick() {
	now := time.Now()

	ip.mu.Lock()
	var expired []string
	active := make([]*BlockedIP, 0, len(ip.banned))
	for _, b := range ip.banned {
		if b.ExpiresAt != nil && b.ExpiresAt.Before(now) {
			expired = append(expired, b.IP)
		} else {
			active = append(active, b)
		}
	}
	ip.mu.Unlock()

	for _, ipStr := range expired {
		ip.bansExpired.Add(1)
		ip.unbanIP(ipStr, "expired")
	}

	// Self-heal: if an earlier firewall op failed (bootstrap error, daemon
	// restart wiping rules), re-assert every active ban. The backends are
	// idempotent, so this is cheap.
	if errs := ip.backend.Sync(active); errs > 0 {
		ip.enforceErrors.Add(int64(errs))
	}

	ip.Reconcile()
}

// ShouldLogBlocked implements log sampling for hits from banned IPs: at
// most one log line per IP per blockedLogSampleInterval.
func (ip *IPSentry) ShouldLogBlocked(ipStr string) bool {
	ip.blockedHits.Add(1)
	ip.mu.Lock()
	defer ip.mu.Unlock()
	last, ok := ip.lastLog[ipStr]
	if ok && time.Since(last) < blockedLogSampleInterval {
		return false
	}
	ip.lastLog[ipStr] = time.Now()
	return true
}

// SentryMetrics is a point-in-time snapshot for the admin API.
type SentryMetrics struct {
	Enabled          bool   `json:"enabled"`
	Backend          string `json:"backend"`
	FailThreshold    int    `json:"fail_threshold"`
	FailWindow       string `json:"fail_window"`
	ActiveBans       int    `json:"active_bans"`
	TrackedIPs       int    `json:"tracked_ips"`
	FailuresSeen     int64  `json:"failures_seen"`
	BlockedHits      int64  `json:"blocked_hits"`
	BansCreated      int64  `json:"bans_created"`
	BansExpired      int64  `json:"bans_expired"`
	ReconcileUnbans  int64  `json:"reconcile_unbans"`
	ManualBans       int64  `json:"manual_bans"`
	ManualUnbans     int64  `json:"manual_unbans"`
	EnforceErrors    int64  `json:"enforce_errors"`
	WhitelistSkipped int64  `json:"whitelist_skipped"`
}

// Metrics returns the current counters.
func (ip *IPSentry) Metrics() SentryMetrics {
	ip.mu.Lock()
	active := len(ip.banned)
	tracked := len(ip.failures)
	ip.mu.Unlock()
	return SentryMetrics{
		Enabled:          true,
		Backend:          ip.backend.Name(),
		FailThreshold:    ip.cfg.Threshold(),
		FailWindow:       ip.cfg.Window().String(),
		ActiveBans:       active,
		TrackedIPs:       tracked,
		FailuresSeen:     ip.failuresSeen.Load(),
		BlockedHits:      ip.blockedHits.Load(),
		BansCreated:      ip.bansCreated.Load(),
		BansExpired:      ip.bansExpired.Load(),
		ReconcileUnbans:  ip.reconcileUnbans.Load(),
		ManualBans:       ip.manualBans.Load(),
		ManualUnbans:     ip.manualUnbans.Load(),
		EnforceErrors:    ip.enforceErrors.Load(),
		WhitelistSkipped: ip.whitelistSkipped.Load(),
	}
}

// ListBans returns active bans, or full history when includeHistory is set.
func (ip *IPSentry) ListBans(includeHistory bool) []BlockedIP {
	if ip.server != nil && ip.server.DB != nil {
		var rows []BlockedIP
		q := ip.server.DB.Order("banned_at DESC")
		if !includeHistory {
			q = q.Where("active = ?", true)
		}
		if err := q.Find(&rows).Error; err == nil {
			return rows
		}
	}
	// DB-less fallback (tests): serve from the in-memory cache.
	ip.mu.Lock()
	out := make([]BlockedIP, 0, len(ip.banned))
	for _, b := range ip.banned {
		out = append(out, *b)
	}
	ip.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].BannedAt.After(out[j].BannedAt) })
	return out
}
