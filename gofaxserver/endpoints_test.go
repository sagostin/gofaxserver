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
