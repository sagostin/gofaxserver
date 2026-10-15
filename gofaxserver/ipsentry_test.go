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

package gofaxserver

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gofaxserver/gofaxlib"
)

// fakeSentryBackend records enforcement ops without touching a firewall.
type fakeSentryBackend struct {
	mu     sync.Mutex
	banned map[string]*time.Time
	synced int
}

func newFakeSentryBackend() *fakeSentryBackend {
	return &fakeSentryBackend{banned: make(map[string]*time.Time)}
}

func (f *fakeSentryBackend) Name() string     { return "fake" }
func (f *fakeSentryBackend) Bootstrap() error { return nil }
func (f *fakeSentryBackend) Ban(ip string, until *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.banned[ip] = until
	return nil
}
func (f *fakeSentryBackend) Unban(ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.banned, ip)
	return nil
}
func (f *fakeSentryBackend) Sync(active []*BlockedIP) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced++
	return 0
}
func (f *fakeSentryBackend) isBanned(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.banned[ip]
	return ok
}

// newTestSentry builds a sentry with the fake backend and no DB.
func newTestSentry(t *testing.T, s *Server, cfg gofaxlib.SecurityConfig) *IPSentry {
	t.Helper()
	oldCfg := gofaxlib.Config.Security
	gofaxlib.Config.Security = cfg
	t.Cleanup(func() { gofaxlib.Config.Security = oldCfg })

	sentry, err := newIPSentry(s)
	if err != nil {
		t.Fatalf("newIPSentry: %v", err)
	}
	if sentry == nil {
		t.Fatal("expected sentry, got nil (disabled?)")
	}
	fake := newFakeSentryBackend()
	sentry.backend = fake
	return sentry
}

func testSentryConfig() gofaxlib.SecurityConfig {
	return gofaxlib.SecurityConfig{
		Enabled:       true,
		Backend:       "log-only",
		FailThreshold: 3,
		FailWindow:    "60s",
		BanDurations:  []string{"10m", "1h", "24h"},
	}
}

func TestSentryDisabledByDefault(t *testing.T) {
	old := gofaxlib.Config.Security
	gofaxlib.Config.Security = gofaxlib.SecurityConfig{}
	t.Cleanup(func() { gofaxlib.Config.Security = old })
	sentry, err := newIPSentry(&Server{})
	if err != nil || sentry != nil {
		t.Fatalf("expected nil sentry when disabled, got %v %v", sentry, err)
	}
}

func TestSentryBansAfterThreshold(t *testing.T) {
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	fake := sentry.backend.(*fakeSentryBackend)
	ipStr := "185.213.155.232"

	for i := 0; i < 2; i++ {
		sentry.RecordFailure(ipStr)
		if sentry.IsBanned(ipStr) {
			t.Fatalf("banned below threshold after %d failures", i+1)
		}
	}
	sentry.RecordFailure(ipStr)
	if !sentry.IsBanned(ipStr) {
		t.Fatal("not banned at threshold")
	}
	if !fake.isBanned(ipStr) {
		t.Fatal("backend ban not enforced")
	}
	if got := sentry.Metrics().BansCreated; got != 1 {
		t.Fatalf("BansCreated = %d, want 1", got)
	}
}

func TestSentryWindowExpiry(t *testing.T) {
	s := &Server{}
	cfg := testSentryConfig()
	cfg.FailWindow = "50ms"
	sentry := newTestSentry(t, s, cfg)
	ipStr := "203.0.113.7"

	sentry.RecordFailure(ipStr)
	sentry.RecordFailure(ipStr)
	time.Sleep(60 * time.Millisecond) // window lapses
	sentry.RecordFailure(ipStr)
	if sentry.IsBanned(ipStr) {
		t.Fatal("stale failures outside the window must not count toward a ban")
	}
}

func TestSentryEscalationAndPermanent(t *testing.T) {
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	fake := sentry.backend.(*fakeSentryBackend)
	ipStr := "198.51.100.23"

	trip := func() {
		for i := 0; i < 3; i++ {
			sentry.RecordFailure(ipStr)
		}
		if !sentry.IsBanned(ipStr) {
			t.Fatal("expected ban after threshold")
		}
	}

	// Offense 1: 10m ban.
	trip()
	b1 := sentry.banned[ipStr]
	if b1.ExpiresAt == nil || b1.OffenseCount != 1 {
		t.Fatalf("offense 1 wrong: %+v", b1)
	}
	if d := time.Until(*b1.ExpiresAt); d > 10*time.Minute || d < 9*time.Minute {
		t.Fatalf("offense 1 duration ~10m expected, got %s", d)
	}

	// Expire it, offend again → offense 2 = 1h.
	sentry.unbanIP(ipStr, "expired")
	trip()
	b2 := sentry.banned[ipStr]
	if b2.OffenseCount != 2 || b2.ExpiresAt == nil {
		t.Fatalf("offense 2 wrong: %+v", b2)
	}
	if d := time.Until(*b2.ExpiresAt); d > time.Hour || d < 59*time.Minute {
		t.Fatalf("offense 2 duration ~1h expected, got %s", d)
	}

	// Offenses 3 (24h) then 4 → permanent (nil ExpiresAt).
	sentry.unbanIP(ipStr, "expired")
	trip()
	sentry.unbanIP(ipStr, "expired")
	trip()
	b4 := sentry.banned[ipStr]
	if b4.OffenseCount != 4 || b4.ExpiresAt != nil {
		t.Fatalf("offense 4 should be permanent: %+v", b4)
	}
	fake.mu.Lock()
	until := fake.banned[ipStr]
	fake.mu.Unlock()
	if until != nil {
		t.Fatal("permanent ban must be enforced without a timeout")
	}
}

func TestSentryWhitelist(t *testing.T) {
	s := &Server{}
	cfg := testSentryConfig()
	cfg.WhitelistCIDRs = []string{"10.0.0.0/8", "203.0.113.9"}
	sentry := newTestSentry(t, s, cfg)

	for i := 0; i < 10; i++ {
		sentry.RecordFailure("10.1.2.3")    // inside /8
		sentry.RecordFailure("203.0.113.9") // bare IP entry
	}
	if sentry.IsBanned("10.1.2.3") || sentry.IsBanned("203.0.113.9") {
		t.Fatal("whitelisted IPs must never be banned")
	}
	if got := sentry.Metrics().WhitelistSkipped; got != 20 {
		t.Fatalf("WhitelistSkipped = %d, want 20", got)
	}
}

func TestSentryNeverBansEndpointIP(t *testing.T) {
	s := &Server{GatewayEndpointsACL: []string{"pbx_acme:192.0.2.1"}}
	sentry := newTestSentry(t, s, testSentryConfig())

	for i := 0; i < 10; i++ {
		sentry.RecordFailure("192.0.2.1")
	}
	if sentry.IsBanned("192.0.2.1") {
		t.Fatal("an IP matching a gateway ACL must never be banned")
	}
}

func TestSentryReconcileUnbansNewEndpoint(t *testing.T) {
	// IP gets banned, then an operator adds it as a customer endpoint —
	// the reconcile hook must release it.
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	fake := sentry.backend.(*fakeSentryBackend)
	ipStr := "185.213.155.232"

	for i := 0; i < 3; i++ {
		sentry.RecordFailure(ipStr)
	}
	if !sentry.IsBanned(ipStr) {
		t.Fatal("setup: expected ban")
	}

	s.GatewayEndpointsACL = []string{"pbx_new:" + ipStr}
	sentry.Reconcile()

	if sentry.IsBanned(ipStr) {
		t.Fatal("reconcile must unban an IP that became an endpoint")
	}
	if fake.isBanned(ipStr) {
		t.Fatal("reconcile must remove the firewall rule")
	}
	if got := sentry.Metrics().ReconcileUnbans; got != 1 {
		t.Fatalf("ReconcileUnbans = %d, want 1", got)
	}
}

func TestSentryJanitorExpiry(t *testing.T) {
	s := &Server{}
	cfg := testSentryConfig()
	cfg.BanDurations = []string{"40ms"}
	sentry := newTestSentry(t, s, cfg)
	fake := sentry.backend.(*fakeSentryBackend)
	ipStr := "198.51.100.77"

	for i := 0; i < 3; i++ {
		sentry.RecordFailure(ipStr)
	}
	if !sentry.IsBanned(ipStr) {
		t.Fatal("setup: expected ban")
	}
	time.Sleep(60 * time.Millisecond)
	sentry.janitorTick()
	if sentry.IsBanned(ipStr) {
		t.Fatal("janitor must expire timed-out bans")
	}
	if fake.isBanned(ipStr) {
		t.Fatal("janitor must remove expired firewall rules")
	}
	if got := sentry.Metrics().BansExpired; got != 1 {
		t.Fatalf("BansExpired = %d, want 1", got)
	}
}

func TestSentryBlockedLogSampling(t *testing.T) {
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	ipStr := "203.0.113.99"

	if !sentry.ShouldLogBlocked(ipStr) {
		t.Fatal("first blocked hit should be logged")
	}
	if sentry.ShouldLogBlocked(ipStr) {
		t.Fatal("second hit within the sample interval must be suppressed")
	}
	// Force the sample window to lapse.
	sentry.mu.Lock()
	sentry.lastLog[ipStr] = time.Now().Add(-2 * blockedLogSampleInterval)
	sentry.mu.Unlock()
	if !sentry.ShouldLogBlocked(ipStr) {
		t.Fatal("hit after the sample interval should be logged again")
	}
	if got := sentry.Metrics().BlockedHits; got != 3 {
		t.Fatalf("BlockedHits = %d, want 3 (suppressed hits still counted)", got)
	}
}

func TestSentryManualBanUnban(t *testing.T) {
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	fake := sentry.backend.(*fakeSentryBackend)
	ipStr := "192.0.2.55"

	if err := sentry.BanManual(ipStr, "test", nil); err != nil {
		t.Fatalf("manual ban: %v", err)
	}
	if !sentry.IsBanned(ipStr) || !fake.isBanned(ipStr) {
		t.Fatal("manual ban not applied")
	}
	if sentry.banned[ipStr].ExpiresAt != nil {
		t.Fatal("manual ban without duration must be permanent")
	}
	if err := sentry.BanManual(ipStr, "again", nil); err == nil {
		t.Fatal("double ban must be rejected")
	}
	if !sentry.UnbanIP(ipStr) {
		t.Fatal("unban of banned IP must succeed")
	}
	if sentry.IsBanned(ipStr) || fake.isBanned(ipStr) {
		t.Fatal("unban did not release the IP")
	}
	if sentry.UnbanIP(ipStr) {
		t.Fatal("unban of unbanned IP must report false")
	}
}

func TestSentryManualBanWhitelisted(t *testing.T) {
	s := &Server{}
	cfg := testSentryConfig()
	cfg.WhitelistCIDRs = []string{"192.0.2.0/24"}
	sentry := newTestSentry(t, s, cfg)

	if err := sentry.BanManual("192.0.2.10", "test", nil); err == nil {
		t.Fatal("manual ban of whitelisted IP must be rejected")
	}
}

func TestSentryInvalidIPIgnored(t *testing.T) {
	s := &Server{}
	sentry := newTestSentry(t, s, testSentryConfig())
	for i := 0; i < 10; i++ {
		sentry.RecordFailure("not-an-ip")
	}
	if got := sentry.Metrics().FailuresSeen; got != 0 {
		t.Fatalf("unparseable IPs must be ignored entirely, FailuresSeen = %d", got)
	}
}

func TestNftTimeoutAndSetSelection(t *testing.T) {
	if got := nftTimeout(90 * time.Minute); got != "5400s" {
		t.Fatalf("nftTimeout = %q", got)
	}
	if nftSetFor("192.0.2.1") != "blocklist4" || nftSetFor("2001:db8::1") != "blocklist6" {
		t.Fatal("nft set selection wrong")
	}
}

// TestNftBanSelfHealsOnMissingTable reproduces the production failure where
// the gofax_sentry table was never created (startup bootstrap failed): the
// first Ban errors with "No such file or directory", the backend must
// re-bootstrap and retry, and the ban must ultimately succeed.
func TestNftBanSelfHealsOnMissingTable(t *testing.T) {
	tableExists := false
	var calls []string
	old := sentryRunner
	sentryRunner = func(name string, args []string, stdin string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "nft" && len(args) >= 3 && args[0] == "list":
			if !tableExists {
				return errors.New("No such file or directory")
			}
			return nil
		case name == "nft" && len(args) == 2 && args[0] == "-f":
			if !tableExists {
				tableExists = true // bootstrap script creates the table
			}
			return nil
		case name == "nft" && args[0] == "add":
			if !tableExists {
				return errors.New("Error: No such file or directory")
			}
			return nil
		}
		return nil
	}
	t.Cleanup(func() { sentryRunner = old })

	b := &nftBackend{}
	exp := time.Now().Add(time.Hour)
	if err := b.Ban("72.142.150.115", &exp); err != nil {
		t.Fatalf("Ban must self-heal on missing table: %v", err)
	}
	if !tableExists {
		t.Fatal("bootstrap never ran")
	}
	// Expect: failed add → list (miss) → -f bootstrap → retried add.
	var adds, bootstraps int
	for _, c := range calls {
		if strings.HasPrefix(c, "nft add element") {
			adds++
		}
		if c == "nft -f -" {
			bootstraps++
		}
	}
	if adds != 2 || bootstraps != 1 {
		t.Fatalf("expected 2 add attempts + 1 bootstrap, got %d/%d: %v", adds, bootstraps, calls)
	}

	// Second ban with the table present: single add, no bootstrap.
	calls = nil
	if err := b.Ban("198.51.100.9", nil); err != nil {
		t.Fatalf("Ban with existing table: %v", err)
	}
	for _, c := range calls {
		if c == "nft -f -" {
			t.Fatal("bootstrap must not re-run when the table exists")
		}
	}
}
