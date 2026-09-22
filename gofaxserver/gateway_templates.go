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
