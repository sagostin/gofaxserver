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

import _ "embed"

// Embedded seed templates, inserted into the gateway_templates table on first
// startup (see seedGatewayTemplates). They mirror
// examples/freeswitch/gateways/{sbc,pbx}_example.xml, converted to Go
// text/template syntax.

//go:embed templates/gateways/sbc.xml
var sbcTemplateXML string

//go:embed templates/gateways/pbx.xml
var pbxTemplateXML string

var defaultGatewayTemplates = []struct {
	name        string
	description string
	body        string
}{
	{
		name:        "sbc",
		description: "Upstream carrier / SBC trunk (T.38 capable). IP-auth by default; set username/password + register for registered trunks.",
		body:        sbcTemplateXML,
	},
	{
		name:        "pbx",
		description: "Customer PBX / downstream system (G.711 only). IP-auth by default; set username/password + register for registered PBXs.",
		body:        pbxTemplateXML,
	},
}
