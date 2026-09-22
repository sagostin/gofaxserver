package gofaxserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

func TestTemplateVariablesExtraction(t *testing.T) {
	vars, err := TemplateVariables(defaultGatewayTemplates[0].body)
	if err != nil {
		t.Fatalf("parse sbc template: %v", err)
	}
	joined := strings.Join(vars, ",")
	for _, want := range []string{"name", "realm", "extension", "register", "username", "password"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sbc template should reference %q, got %v", want, vars)
		}
	}
}

func TestTemplateVariablesInvalid(t *testing.T) {
	if _, err := TemplateVariables(`{{.realm`); err == nil {
		t.Fatal("unclosed action should fail parsing")
	}
}

func TestRenderSbcTemplateIPAuth(t *testing.T) {
	out, err := renderGatewayTemplate(defaultGatewayTemplates[0].body, map[string]interface{}{
		"name":      "sbc_carrier",
		"realm":     "sbc.example.com",
		"extension": "auto_to_user",
		"register":  false,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `name="sbc_carrier"`) || !strings.Contains(out, `value="sbc.example.com"`) {
		t.Fatalf("rendered output missing name/realm:\n%s", out)
	}
	if strings.Contains(out, "username") {
		t.Fatalf("IP-auth render must omit username/password:\n%s", out)
	}
	if !strings.Contains(out, `name="register" value="false"`) {
		t.Fatalf("register should render false:\n%s", out)
	}
}

func TestRenderPbxTemplateRegistered(t *testing.T) {
	out, err := renderGatewayTemplate(defaultGatewayTemplates[1].body, map[string]interface{}{
		"name":      "pbx_acme",
		"realm":     "192.0.2.10",
		"extension": "auto_to_user",
		"register":  true,
		"username":  "faxuser",
		"password":  "s3cret",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{`value="faxuser"`, `value="s3cret"`, `name="register" value="true"`} {
		if !strings.Contains(out, want) {
			t.Errorf("registered render missing %s:\n%s", want, out)
		}
	}
}

func TestRenderMissingOptionalVariable(t *testing.T) {
	// Optional vars (username) absent: renders fine, conditional omitted.
	out, err := renderGatewayTemplate(defaultGatewayTemplates[0].body, map[string]interface{}{
		"name":  "sbc_x",
		"realm": "sbc.example.com",
	})
	if err != nil {
		t.Fatalf("optional vars should not fail render: %v", err)
	}
	if strings.Contains(out, "username") {
		t.Fatalf("absent optional var should omit the block:\n%s", out)
	}
}

func TestValidateSpec(t *testing.T) {
	base := GatewayProvisionSpec{Name: "pbx_acme", Params: map[string]interface{}{"realm": "192.0.2.10"}}
	if err := validateSpec(base); err != nil {
		t.Fatalf("valid spec: %v", err)
	}

	noRealm := GatewayProvisionSpec{Name: "pbx_acme", Params: map[string]interface{}{}}
	if err := validateSpec(noRealm); err == nil {
		t.Fatal("missing realm must fail")
	}

	registerNoAuth := GatewayProvisionSpec{Name: "sbc_x", Params: map[string]interface{}{
		"realm": "sbc.example.com", "register": true,
	}}
	if err := validateSpec(registerNoAuth); err == nil {
		t.Fatal("register=true without username/password must fail")
	}
}

func TestValidateGatewayName(t *testing.T) {
	for _, ok := range []string{"pbx_acme", "sbc_easybell", "gw01"} {
		if err := validateGatewayName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "PBX_ACME", "pbx acme", "pbx;rm", "../etc", "pbx.xml", "sbc-gw"} {
		if err := validateGatewayName(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestWriteGatewayFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path, err := writeGatewayFile(dir, "pbx_test", "<include/>\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if filepath.Base(path) != "pbx_test.xml" {
		t.Fatalf("unexpected path %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "<include/>\n" {
		t.Fatalf("read back: %v %q", err, data)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file, got %d", len(entries))
	}
}

func TestEndpointValue(t *testing.T) {
	// Explicit endpoint_ip wins over realm.
	if got := endpointValue("sbc_gw", "203.0.113.5", map[string]interface{}{"realm": "sbc.example.com"}); got != "sbc_gw:203.0.113.5" {
		t.Errorf("explicit ip: got %q", got)
	}
	// Falls back to realm.
	if got := endpointValue("pbx_acme", "", map[string]interface{}{"realm": "192.0.2.10"}); got != "pbx_acme:192.0.2.10" {
		t.Errorf("realm fallback: got %q", got)
	}
	// No ip at all → bare name.
	if got := endpointValue("pbx_acme", "", map[string]interface{}{}); got != "pbx_acme" {
		t.Errorf("bare name: got %q", got)
	}
}

func TestDialplanAccessorNeverNil(t *testing.T) {
	// A Server that never stored a manager (e.g. DB load failed at startup)
	// must still return a working manager with the built-in defaults.
	s := &Server{}
	d := s.Dialplan()
	if d == nil {
		t.Fatal("Dialplan() must never return nil")
	}
	if got := d.ApplyTransformationRules("15551234567"); got != "5551234567" {
		t.Fatalf("fallback defaults not applied: got %q", got)
	}
}

func TestEndpointFromSpec(t *testing.T) {
	ep, err := endpointFromSpec(GatewayProvisionSpec{
		Name:   "pbx_acme",
		Scope:  "tenant",
		TypeID: 7,
		Params: map[string]interface{}{"realm": "192.0.2.10"},
		Bridge: true,
	})
	if err != nil {
		t.Fatalf("endpointFromSpec: %v", err)
	}
	if ep.Endpoint != "pbx_acme:192.0.2.10" || ep.EndpointType != "gateway" || !ep.Bridge || ep.TypeID != 7 {
		t.Fatalf("unexpected endpoint: %+v", ep)
	}

	// Explicit endpoint_ip wins over realm.
	ep, err = endpointFromSpec(GatewayProvisionSpec{
		Name:       "sbc_gw",
		Scope:      "global",
		TypeID:     42, // must be zeroed for global
		EndpointIP: "203.0.113.5",
		Params:     map[string]interface{}{"realm": "sbc.example.com"},
	})
	if err != nil {
		t.Fatalf("endpointFromSpec: %v", err)
	}
	if ep.Endpoint != "sbc_gw:203.0.113.5" || ep.TypeID != 0 {
		t.Fatalf("unexpected endpoint: %+v", ep)
	}

	if _, err := endpointFromSpec(GatewayProvisionSpec{Name: "x", Scope: "bogus"}); err == nil {
		t.Fatal("invalid scope should fail")
	}
}

func TestProvisionDefaults(t *testing.T) {
	p := provisionDefaults(map[string]interface{}{"realm": "x"})
	if p["extension"] != "auto_to_user" || p["register"] != false {
		t.Fatalf("defaults not applied: %+v", p)
	}
	p = provisionDefaults(map[string]interface{}{"extension": "1000", "register": true})
	if p["extension"] != "1000" || p["register"] != true {
		t.Fatalf("explicit values must be preserved: %+v", p)
	}
}

func TestRenderEscapesXMLInjection(t *testing.T) {
	// A hostile realm must be escaped, not injected as markup.
	out, err := renderGatewayTemplate(defaultGatewayTemplates[0].body, map[string]interface{}{
		"name":  "sbc_x",
		"realm": `evil"/><param name="sip-port" value="9999"/><x a="`,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, `<param name="sip-port"`) {
		t.Fatalf("injection succeeded:\n%s", out)
	}
	if !strings.Contains(out, "&quot;") && !strings.Contains(out, "&#34;") {
		t.Fatalf("quotes should be escaped:\n%s", out)
	}
}

func TestParamsEncryptionAndMasking(t *testing.T) {
	params := map[string]interface{}{
		"realm":    "198.51.100.20",
		"password": "s3cret",
		"register": true,
	}
	enc, err := encodeParams(params)
	if err != nil {
		t.Fatalf("encodeParams: %v", err)
	}
	if strings.Contains(enc, "s3cret") {
		t.Fatal("stored params must not contain the plaintext password")
	}

	// Round-trip.
	dec, err := decodeParams(enc)
	if err != nil {
		t.Fatalf("decodeParams: %v", err)
	}
	if dec["password"] != "s3cret" || dec["realm"] != "198.51.100.20" {
		t.Fatalf("round-trip mismatch: %+v", dec)
	}

	// Masked output hides secrets but keeps structure.
	masked := maskedParamsJSON(enc)
	if strings.Contains(masked, "s3cret") {
		t.Fatalf("masked output leaks password: %s", masked)
	}
	if !strings.Contains(masked, maskSentinel) || !strings.Contains(masked, "198.51.100.20") {
		t.Fatalf("masked output wrong: %s", masked)
	}

	// Legacy plaintext rows still decode.
	legacy, err := decodeParams(`{"realm":"192.0.2.1"}`)
	if err != nil || legacy["realm"] != "192.0.2.1" {
		t.Fatalf("legacy plaintext decode: %v %+v", err, legacy)
	}
}

func TestFsGatewayACLExactMatch(t *testing.T) {
	s := &Server{GatewayEndpointsACL: []string{"pbx_acme:192.168.1.10", "sbc_gw:203.0.113.5"}}

	if got, err := s.fsGatewayACL("192.168.1.10"); err != nil || got != "pbx_acme:192.168.1.10" {
		t.Errorf("exact IP should match: %q %v", got, err)
	}
	// Substring attacks must fail.
	for _, ip := range []string{"92.168.1.1", "192.168.1.1", "192.168.1.100", "2.168.1.10"} {
		if _, err := s.fsGatewayACL(ip); err == nil {
			t.Errorf("substring IP %q must not match ACL", ip)
		}
	}
	// Legacy IP-only entries still work by exact equality.
	s.GatewayEndpointsACL = append(s.GatewayEndpointsACL, "10.0.0.7")
	if _, err := s.fsGatewayACL("10.0.0.7"); err != nil {
		t.Errorf("legacy IP-only entry should match exactly: %v", err)
	}
}

func TestGatewayNameFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pbx_acme.xml")
	if err := os.WriteFile(path, []byte(`<include><gateway name="pbx_acme"><param name="realm" value="1.2.3.4"/></gateway></include>`), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := gatewayNameFromFile(path)
	if err != nil || name != "pbx_acme" {
		t.Fatalf("want pbx_acme, got %q (%v)", name, err)
	}

	bad := filepath.Join(dir, "bad.xml")
	os.WriteFile(bad, []byte(`<include></include>`), 0o644)
	if _, err := gatewayNameFromFile(bad); err == nil {
		t.Fatal("file without gateway name should fail")
	}
}

func TestMonitorTransition(t *testing.T) {
	if changed, _ := monitorTransition("REGED", "REGED"); changed {
		t.Error("same state should not be a transition")
	}
	if changed, level := monitorTransition("REGED", "FAIL_WAIT"); !changed || level != logrus.WarnLevel {
		t.Error("drop from REGED should warn")
	}
	if changed, level := monitorTransition("FAIL_WAIT", "REGED"); !changed || level != logrus.InfoLevel {
		t.Error("recovery to REGED should be info")
	}
	if changed, _ := monitorTransition("", "NOREG"); !changed {
		t.Error("initial observation should count as transition")
	}
}

func TestMonitorInterval(t *testing.T) {
	old := gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds
	defer func() { gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds = old }()

	gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds = 0
	if got := monitorInterval(); got != 60*time.Second {
		t.Errorf("default should be 60s, got %s", got)
	}
	gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds = 5
	if got := monitorInterval(); got != 5*time.Second {
		t.Errorf("got %s", got)
	}
	gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds = -1
	if got := monitorInterval(); got != 0 {
		t.Errorf("negative should disable, got %s", got)
	}
}

func TestGatewayProfileDefault(t *testing.T) {
	old := gofaxlib.Config.FreeSwitch.GatewayProfile
	defer func() { gofaxlib.Config.FreeSwitch.GatewayProfile = old }()

	gofaxlib.Config.FreeSwitch.GatewayProfile = ""
	if got := gatewayProfile(); got != "fax" {
		t.Errorf("default profile should be fax, got %q", got)
	}
	gofaxlib.Config.FreeSwitch.GatewayProfile = "external"
	if got := gatewayProfile(); got != "external" {
		t.Errorf("got %q", got)
	}
}

func TestCompileDialplanRulesSkipsBadPatterns(t *testing.T) {
	rows := []DialplanRule{
		{ID: 1, Pattern: `^1(\d{10}).*$`, Replacement: "$1", Enabled: true},
		{ID: 2, Pattern: `^([0-9`, Replacement: "$1", Enabled: true}, // invalid
	}
	rules := compileDialplanRules(rows)
	if len(rules) != 1 {
		t.Fatalf("bad pattern should be skipped, got %d rules", len(rules))
	}
	d := NewDialplanManager(rules)
	if got := d.ApplyTransformationRules("15551234567"); got != "5551234567" {
		t.Fatalf("remaining rule should apply, got %q", got)
	}
}

func TestDialplanSourceToggle(t *testing.T) {
	old := gofaxlib.Config.Dialplan
	defer func() { gofaxlib.Config.Dialplan = old }()

	gofaxlib.Config.Dialplan = nil
	if dialplanSource() != "config" {
		t.Error("absent section should be config mode")
	}
	gofaxlib.Config.Dialplan = &gofaxlib.DialplanConfig{Source: "db"}
	if dialplanSource() != "db" {
		t.Error("source=db should be db mode")
	}
	gofaxlib.Config.Dialplan = &gofaxlib.DialplanConfig{Source: "config"}
	if dialplanSource() != "config" {
		t.Error("explicit config source")
	}
}
