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
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
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
	for _, ok := range []string{"pbx_acme", "sbc_easybell", "gw01", "PBX_ACME", "pbx_CarterFinancial"} {
		if err := validateGatewayName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "pbx acme", "pbx;rm", "../etc", "pbx.xml", "sbc-gw"} {
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

// fakeACLResolver stubs DNS for ACL resolution tests.
type fakeACLResolver struct {
	ips map[string][]string
	err map[string]error
}

func (f *fakeACLResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if err, ok := f.err[host]; ok {
		return nil, err
	}
	ips, ok := f.ips[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out, nil
}

// withFakeACLResolver swaps the package resolver for the test duration.
func withFakeACLResolver(t *testing.T, f *fakeACLResolver) {
	t.Helper()
	old := aclResolver
	aclResolver = f
	t.Cleanup(func() { aclResolver = old })
}

func TestResolveGatewayACLs(t *testing.T) {
	fake := &fakeACLResolver{
		ips: map[string][]string{"pbx.example.com": {"203.0.113.5", "203.0.113.6"}},
		err: map[string]error{"down.example.com": errors.New("SERVFAIL")},
	}
	withFakeACLResolver(t, fake)

	resolved, failures := resolveGatewayACLs([]string{
		"pbx_acme:pbx.example.com",
		"sbc_ip:198.51.100.20", // literal IP: no DNS
		"10.0.0.7",             // legacy IP-only: no DNS
		"gw_broken:down.example.com",
	})

	got := resolved["pbx_acme:pbx.example.com"]
	if len(got) != 2 || !slices.Contains(got, "203.0.113.5") || !slices.Contains(got, "203.0.113.6") {
		t.Errorf("hostname should resolve to both A records, got %v", got)
	}
	if _, ok := resolved["sbc_ip:198.51.100.20"]; ok {
		t.Error("literal IP entries must not be resolved")
	}
	if _, ok := resolved["10.0.0.7"]; ok {
		t.Error("legacy IP-only entries must not be resolved")
	}
	if _, ok := failures["gw_broken:down.example.com"]; !ok {
		t.Error("failed lookup should be reported in failures")
	}
	if _, ok := resolved["gw_broken:down.example.com"]; ok {
		t.Error("failed lookup must not produce a resolved entry")
	}
}

func TestFsGatewayACLHostnameMatch(t *testing.T) {
	s := &Server{
		GatewayEndpointsACL: []string{"pbx_acme:pbx.example.com", "sbc_gw:198.51.100.20"},
		GatewayACLResolved:  map[string][]string{"pbx_acme:pbx.example.com": {"203.0.113.5", "203.0.113.6"}},
	}

	// Any resolved A record matches, and returns the hostname entry.
	for _, ip := range []string{"203.0.113.5", "203.0.113.6"} {
		if got, err := s.fsGatewayACL(ip); err != nil || got != "pbx_acme:pbx.example.com" {
			t.Errorf("resolved IP %s should match hostname entry: %q %v", ip, got, err)
		}
	}
	// Unresolved hostname entries fail closed.
	if _, err := s.fsGatewayACL("198.51.100.99"); err == nil {
		t.Error("IP outside the resolved set must not match")
	}
	// Exact-IP entries still take the fast path.
	if got, err := s.fsGatewayACL("198.51.100.20"); err != nil || got != "sbc_gw:198.51.100.20" {
		t.Errorf("literal IP entry should still match: %q %v", got, err)
	}
}

func TestRefreshGatewayACLResolution(t *testing.T) {
	fake := &fakeACLResolver{
		ips: map[string][]string{"pbx.example.com": {"203.0.113.5"}},
	}
	withFakeACLResolver(t, fake)

	s := &Server{
		LogManager:          &gofaxlib.LogManager{},
		GatewayEndpointsACL: []string{"pbx_acme:pbx.example.com"},
	}

	// First refresh populates the cache.
	s.refreshGatewayACLResolution()
	if got, err := s.fsGatewayACL("203.0.113.5"); err != nil || got != "pbx_acme:pbx.example.com" {
		t.Fatalf("after refresh, resolved IP should match: %q %v", got, err)
	}

	// Far end moves: next refresh picks up the new IP automatically.
	fake.ips["pbx.example.com"] = []string{"203.0.113.9"}
	s.refreshGatewayACLResolution()
	if _, err := s.fsGatewayACL("203.0.113.9"); err != nil {
		t.Errorf("new far-end IP should match after refresh: %v", err)
	}
	if _, err := s.fsGatewayACL("203.0.113.5"); err == nil {
		t.Error("old far-end IP should no longer match after refresh")
	}

	// DNS outage: last-good IPs are kept.
	fake.err = map[string]error{"pbx.example.com": errors.New("SERVFAIL")}
	s.refreshGatewayACLResolution()
	if _, err := s.fsGatewayACL("203.0.113.9"); err != nil {
		t.Errorf("DNS failure must keep last-good IPs: %v", err)
	}
}

func TestRefreshGatewayACLResolutionPrunesRemovedEntries(t *testing.T) {
	fake := &fakeACLResolver{
		ips: map[string][]string{"pbx.example.com": {"203.0.113.5"}},
	}
	withFakeACLResolver(t, fake)

	s := &Server{
		LogManager:          &gofaxlib.LogManager{},
		GatewayEndpointsACL: []string{"pbx_acme:pbx.example.com"},
	}
	s.refreshGatewayACLResolution()

	// Endpoint removed: cache entry must be pruned on the next refresh.
	s.GatewayEndpointsACL = []string{}
	s.refreshGatewayACLResolution()
	if _, err := s.fsGatewayACL("203.0.113.5"); err == nil {
		t.Error("removed endpoint must not match after refresh")
	}
}

func TestEqualIPs(t *testing.T) {
	if !equalIPs([]string{"1.1.1.1", "2.2.2.2"}, []string{"2.2.2.2", "1.1.1.1"}) {
		t.Error("order-insensitive comparison expected")
	}
	if equalIPs([]string{"1.1.1.1"}, []string{"1.1.1.1", "2.2.2.2"}) {
		t.Error("different sets must not compare equal")
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

// TestFromEndpointParsing covers the endpoint-value parsing used by
// ProvisionGatewayFromEndpoint: "name:ip" → gateway name + default realm.
func TestFromEndpointParsing(t *testing.T) {
	cases := []struct {
		value     string
		wantName  string
		wantIP    string
		nameValid bool
	}{
		{"pbx_acme:192.0.2.10", "pbx_acme", "192.0.2.10", true},
		{"sbc_gw", "sbc_gw", "", true},
		{"pbx_CarterFinancial:216.138.253.72", "pbx_CarterFinancial", "216.138.253.72", true}, // imported mixed-case name
		{"weird name:1.2.3.4", "weird name", "1.2.3.4", false},
	}
	for _, c := range cases {
		name, ip, _ := strings.Cut(c.value, ":")
		if name != c.wantName || ip != c.wantIP {
			t.Errorf("cut %q: got (%q, %q), want (%q, %q)", c.value, name, ip, c.wantName, c.wantIP)
		}
		if err := validateGatewayName(name); (err == nil) != c.nameValid {
			t.Errorf("validateGatewayName(%q) valid=%v, want %v", name, err == nil, c.nameValid)
		}
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
