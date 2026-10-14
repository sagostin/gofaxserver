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

// ipsentry_backend.go — firewall enforcement backends for the IP sentry.
//
// All backends shell out to the host's firewall tooling. gofaxserver runs
// with network_mode: host (or directly on the host), so nftables/ipset
// operations affect the host network namespace. Required privileges:
// CAP_NET_ADMIN (container: `cap_add: NET_ADMIN`; the image setcaps the
// nft/ipset binaries so the non-root appuser can execute them; systemd
// host installs: AmbientCapabilities=CAP_NET_ADMIN).

package gofaxserver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// sentryBackend enforces bans in the host firewall. All implementations
// must be idempotent: Ban of an already-banned IP and Unban of an
// unbanned IP must succeed (or fail harmlessly).
type sentryBackend interface {
	Name() string
	// Bootstrap creates the dedicated table/set and drop chain. May
	// flush and recreate existing state; persisted bans are re-applied
	// afterwards by the sentry.
	Bootstrap() error
	// Ban blocks the IP until the given time (nil = permanent).
	Ban(ip string, until *time.Time) error
	// Unban removes the block for the IP.
	Unban(ip string) error
	// Sync re-asserts every active ban (self-healing after a firewall
	// flush or a failed earlier op). Returns the number of errors.
	Sync(active []*BlockedIP) int
}

// sentryCmdTimeout bounds a single firewall command.
const sentryCmdTimeout = 10 * time.Second

// sentryRunner executes a firewall command. Package-level so tests can
// capture invocations instead of touching the real firewall.
var sentryRunner = func(name string, args []string, stdin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), sentryCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

func newSentryBackend(name string, s *IPSentry) (sentryBackend, error) {
	switch name {
	case "nft":
		return &nftBackend{}, nil
	case "ipset":
		return &ipsetBackend{}, nil
	case "log-only":
		return &logOnlyBackend{sentry: s}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (want nft, ipset, or log-only)", name)
	}
}

// nftTimeout renders a ban duration as an nftables element timeout.
func nftTimeout(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}

// nftSetFor picks the per-family set name.
func nftSetFor(ip string) string {
	if strings.Contains(ip, ":") {
		return "blocklist6"
	}
	return "blocklist4"
}

// ---------------------------------------------------------------------------
// nftables backend
// ---------------------------------------------------------------------------

// nftBootstrapScript recreates the dedicated table. `destroy` (unlike
// `delete`) succeeds when the table does not exist yet. The input chain
// hooks at `filter - 10` so drops happen before ufw/other filter-priority
// chains accept the packet.
const nftBootstrapScript = `destroy table inet gofax_sentry
table inet gofax_sentry {
	set blocklist4 {
		type ipv4_addr
		flags timeout
	}
	set blocklist6 {
		type ipv6_addr
		flags timeout
	}
	chain input {
		type filter hook input priority filter - 10; policy accept;
		ip saddr @blocklist4 drop
		ip6 saddr @blocklist6 drop
	}
}
`

type nftBackend struct{}

func (b *nftBackend) Name() string { return "nft" }

func (b *nftBackend) Bootstrap() error {
	return sentryRunner("nft", []string{"-f", "-"}, nftBootstrapScript)
}

func (b *nftBackend) Ban(ip string, until *time.Time) error {
	set := nftSetFor(ip)
	elem := ip
	if until != nil {
		elem = fmt.Sprintf("%s timeout %s", ip, nftTimeout(time.Until(*until)))
	}
	return sentryRunner("nft", []string{"add", "element", "inet", "gofax_sentry", set, "{ " + elem + " }"}, "")
}

func (b *nftBackend) Unban(ip string) error {
	// Missing elements are not an error for us.
	_ = sentryRunner("nft", []string{"delete", "element", "inet", "gofax_sentry", nftSetFor(ip), "{ " + ip + " }"}, "")
	return nil
}

func (b *nftBackend) Sync(active []*BlockedIP) int {
	if len(active) == 0 {
		return 0
	}
	var sb strings.Builder
	for _, rec := range active {
		elem := rec.IP
		if rec.ExpiresAt != nil {
			if d := time.Until(*rec.ExpiresAt); d > 0 {
				elem = fmt.Sprintf("%s timeout %s", rec.IP, nftTimeout(d))
			}
		}
		fmt.Fprintf(&sb, "add element inet gofax_sentry %s { %s }\n", nftSetFor(rec.IP), elem)
	}
	if err := sentryRunner("nft", []string{"-f", "-"}, sb.String()); err != nil {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// ipset + iptables backend
// ---------------------------------------------------------------------------

const ipsetName = "gofax_block"

type ipsetBackend struct{}

func (b *ipsetBackend) Name() string { return "ipset" }

func (b *ipsetBackend) Bootstrap() error {
	// timeout 0 on the set enables per-element timeouts.
	if err := sentryRunner("ipset", []string{"create", "-exist", ipsetName, "hash:ip", "family", "inet", "timeout", "0"}, ""); err != nil {
		return err
	}
	// Ensure the DROP rule exists exactly once, ahead of other INPUT rules.
	if err := sentryRunner("iptables", []string{"-C", "INPUT", "-m", "set", "--match-set", ipsetName, "src", "-j", "DROP"}, ""); err != nil {
		return sentryRunner("iptables", []string{"-I", "INPUT", "1", "-m", "set", "--match-set", ipsetName, "src", "-j", "DROP"}, "")
	}
	return nil
}

func (b *ipsetBackend) Ban(ip string, until *time.Time) error {
	timeout := "0" // 0 = never expires
	if until != nil {
		timeout = fmt.Sprintf("%d", int64(time.Until(*until).Seconds()))
	}
	return sentryRunner("ipset", []string{"add", "-exist", ipsetName, ip, "timeout", timeout}, "")
}

func (b *ipsetBackend) Unban(ip string) error {
	_ = sentryRunner("ipset", []string{"del", "-exist", ipsetName, ip}, "")
	return nil
}

func (b *ipsetBackend) Sync(active []*BlockedIP) int {
	errs := 0
	for _, rec := range active {
		if err := b.Ban(rec.IP, rec.ExpiresAt); err != nil {
			errs++
		}
	}
	return errs
}

// ---------------------------------------------------------------------------
// log-only backend (detection + log suppression, no firewall changes)
// ---------------------------------------------------------------------------

type logOnlyBackend struct {
	sentry *IPSentry
}

func (b *logOnlyBackend) Name() string { return "log-only" }

func (b *logOnlyBackend) Bootstrap() error { return nil }

func (b *logOnlyBackend) Ban(ip string, until *time.Time) error {
	dur := "permanent"
	if until != nil {
		dur = time.Until(*until).Round(time.Second).String()
	}
	b.sentry.log(logrus.WarnLevel, fmt.Sprintf("[log-only] would ban %s (%s)", ip, dur), map[string]interface{}{"ip": ip, "duration": dur})
	return nil
}

func (b *logOnlyBackend) Unban(ip string) error {
	b.sentry.log(logrus.InfoLevel, fmt.Sprintf("[log-only] would unban %s", ip), map[string]interface{}{"ip": ip})
	return nil
}

func (b *logOnlyBackend) Sync(_ []*BlockedIP) int { return 0 }
