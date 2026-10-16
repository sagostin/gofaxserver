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
	"testing"
)

func TestEndpointGatewayDialstring(t *testing.T) {
	got := endpointGatewayDialstring([]string{"gw1", "gw2"}, "2509550795")
	want := "sofia/gateway/gw1/2509550795,sofia/gateway/gw2/2509550795"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEndpointGatewayDialstringTagged(t *testing.T) {
	got := endpointGatewayDialstringTagged([]string{"carrier1", "carrier2"}, "2509550795")

	// Every leg carries the gateway tag + export_vars, in failover order,
	// with the plain sofia/gateway target intact.
	want := "[gofax_bridge_gw=carrier1,export_vars=gofax_bridge_gw]sofia/gateway/carrier1/2509550795" +
		",[gofax_bridge_gw=carrier2,export_vars=gofax_bridge_gw]sofia/gateway/carrier2/2509550795"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEndpointGatewayDialstringTaggedSingle(t *testing.T) {
	got := endpointGatewayDialstringTagged([]string{"carrier1"}, "2509550795")
	want := "[gofax_bridge_gw=carrier1,export_vars=gofax_bridge_gw]sofia/gateway/carrier1/2509550795"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEndpointGatewayDialstringTaggedEmpty(t *testing.T) {
	if got := endpointGatewayDialstringTagged(nil, "2509550795"); got != "" {
		t.Errorf("expected empty dialstring, got %q", got)
	}
}

func TestEndpointGatewayDialstringOutboundTagged(t *testing.T) {
	got := endpointGatewayDialstringOutboundTagged([]string{"carrier1", "carrier2"}, "2509550795")

	// Every leg carries the gateway tag (no export_vars — the txfax channel
	// IS the gateway leg), in failover order, with the plain sofia/gateway
	// target intact.
	want := "[gofax_gw=carrier1]sofia/gateway/carrier1/2509550795" +
		",[gofax_gw=carrier2]sofia/gateway/carrier2/2509550795"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEndpointGatewayDialstringOutboundTaggedEmpty(t *testing.T) {
	if got := endpointGatewayDialstringOutboundTagged(nil, "2509550795"); got != "" {
		t.Errorf("expected empty dialstring, got %q", got)
	}
}

func TestResolveUpstreamBridgeGateway(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		attempts []bridgeAttempt
		want     string
	}{
		{"answered bridge keeps winning gateway", "telcobridges1",
			[]bridgeAttempt{{Gateway: "telcobridges1", HangupCause: "NORMAL_CLEARING"}}, "telcobridges1"},
		{"failed single attempt resolves to it", "upstream",
			[]bridgeAttempt{{Gateway: "telcobridges2", HangupCause: "USER_BUSY"}}, "telcobridges2"},
		{"failover attributes the last (final-response) gateway", "upstream",
			[]bridgeAttempt{
				{Gateway: "telcobridges1", HangupCause: "NO_ROUTE_DESTINATION"},
				{Gateway: "telcobridges2", HangupCause: "USER_BUSY"},
			}, "telcobridges2"},
		{"no attempts keeps the placeholder", "upstream", nil, "upstream"},
	}
	for _, c := range cases {
		if got := resolveUpstreamBridgeGateway(c.current, c.attempts); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGetTenantByGateway(t *testing.T) {
	s := &Server{
		Tenants: map[uint]*Tenant{
			12: {ID: 12, Name: "TOPS"},
			25: {ID: 25, Name: "monasheeopt"},
		},
		TenantNumbers: map[string]*TenantNumber{
			"2505501123": {ID: 7, TenantID: 25, Number: "2505501123"},
		},
		TenantEndpoints: map[uint][]*Endpoint{
			12: {{Type: "tenant", TypeID: 12, EndpointType: "gateway", Endpoint: "pbx_tops:1.2.3.4"}},
		},
		NumberEndpoints: map[string][]*Endpoint{
			"2505501123": {{Type: "number", TypeID: 7, EndpointType: "gateway", Endpoint: "pbx_monasheeopt"}},
		},
	}

	// Tenant-scoped gateway endpoint (with :ip ACL suffix).
	if tenant, err := s.getTenantByGateway("pbx_tops"); err != nil || tenant.ID != 12 {
		t.Errorf("pbx_tops → %v, %v; want tenant 12", tenant, err)
	}
	// Number-scoped gateway endpoint resolves through its tenant number.
	if tenant, err := s.getTenantByGateway("pbx_monasheeopt"); err != nil || tenant.ID != 25 {
		t.Errorf("pbx_monasheeopt → %v, %v; want tenant 25", tenant, err)
	}
	// Carrier / unknown gateways must never resolve to a tenant.
	for _, gw := range []string{"telcobridges1", "upstream", "pbx_unknown"} {
		if tenant, err := s.getTenantByGateway(gw); err == nil || tenant != nil {
			t.Errorf("%s → %v, nil; want no match", gw, tenant)
		}
	}
}
