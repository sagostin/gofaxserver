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
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

// This file implements API-driven FreeSWITCH gateway provisioning:
// gateway XML files are rendered from database-backed templates, written to
// the configured gateway directory (shared with FreeSWITCH via mount), and
// activated by reloading the sofia profile over the event socket.
//
// Invariant (see docs/GATEWAYS.md): the gateway name must equal the XML
// filename (sans .xml) and the prefix of the linked Endpoint value.

// gatewayNameRe restricts gateway names to filename-safe, dialstring-safe
// characters (e.g. "pbx_acme", "sbc_easybell", "pbx_CarterFinancial").
// Case is preserved verbatim: the same string is used as the XML filename,
// the <gateway name>, the endpoint prefix and the sofia dialstring name.
var gatewayNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// GatewayTemplate is a database-backed FreeSWITCH gateway XML template.
// The body uses Go text/template syntax; variables (e.g. {{.realm}}) are
// extracted and exposed via the admin API so UIs can render a dynamic form.
type GatewayTemplate struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex" json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GatewayConfig records a provisioned FreeSWITCH gateway: which template was
// rendered with which parameters, and which Endpoint row it is linked to.
type GatewayConfig struct {
	ID         uint            `gorm:"primaryKey" json:"id"`
	Name       string          `gorm:"uniqueIndex" json:"name"` // gateway name == filename sans .xml == endpoint prefix
	TemplateID uint            `json:"template_id"`
	Template   GatewayTemplate `json:"template"`
	Params     string          `json:"params"` // JSON object of template variables (encrypted at rest)
	EndpointID uint            `json:"endpoint_id"`
	// Registration state tracking (populated by the monitor for
	// register=true gateways).
	LastState          string     `json:"last_state"`
	LastStateCheckedAt *time.Time `json:"last_state_checked_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// GatewayProvisionSpec is the payload for the combined provision call:
// render + write the gateway XML, reload FreeSWITCH, and create the linked
// endpoint in one operation.
type GatewayProvisionSpec struct {
	Name       string                 `json:"name"`
	TemplateID uint                   `json:"template_id"`
	Params     map[string]interface{} `json:"params"`

	// Linked endpoint fields (mirrors Endpoint).
	Scope    string `json:"type"`     // tenant, number, or global
	TypeID   uint   `json:"type_id"`  // tenant/number id (0 for global)
	Priority uint   `json:"priority"` // 0 = highest; 666 = outbound-only
	Bridge   bool   `json:"bridge"`   // bridge/transcoding mode instead of txfax/rxfax
	// EndpointIP is the public IP appended to the endpoint value
	// ("name:ip") used for inbound ACL matching. Defaults to the "realm"
	// param when empty.
	EndpointIP string `json:"endpoint_ip"`
}

// GatewayStatus pairs a provisioned gateway with its live sofia state.
type GatewayStatus struct {
	Gateway GatewayConfig `json:"gateway"`
	File    string        `json:"file"`           // full path of the rendered XML
	State   string        `json:"state"`          // best-effort parse of `sofia status gateway`
	Exists  bool          `json:"exists_on_disk"` // XML file present in the gateway dir
	// Aliases are number-scoped endpoints routing onto this gateway besides
	// its linked endpoint (see CreateGatewayAlias). Populated by ListGateways.
	Aliases []Endpoint `json:"aliases,omitempty"`
}

// Sentinels for the gateway alias API so handlers can map them to status codes.
var (
	// ErrGatewayNotFound — no provisioned gateway with that name.
	ErrGatewayNotFound = errors.New("gateway not provisioned")
	// ErrGatewayAliasInvalid — bad alias spec (unknown number, etc.).
	ErrGatewayAliasInvalid = errors.New("invalid gateway alias")
	// ErrGatewayAliasExists — the number already routes to this gateway.
	ErrGatewayAliasExists = errors.New("gateway alias already exists")
	// ErrGatewayHasAliases — deprovision refused while aliases reference it.
	ErrGatewayHasAliases = errors.New("gateway has alias endpoints")
)

// GatewayAliasSpec is the payload for CreateGatewayAlias: a number-scoped
// endpoint pointing at an already-provisioned gateway, so a number can fan
// out to a gateway that (typically) already serves its tenant — without
// re-provisioning. Scope is fixed to "number" for now; tenant/global aliasing
// is a documented future provision (see docs/GATEWAYS.md).
type GatewayAliasSpec struct {
	TypeID   uint `json:"type_id"`  // TenantNumber.ID
	Priority uint `json:"priority"` // 0 = highest; 666 = no inbound delivery
	Bridge   bool `json:"bridge"`   // bridge inbound calls instead of rxfax (no file → no portal delivery)
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

// maskSentinel replaces sensitive param values in API responses.
const maskSentinel = "********"

// isSensitiveParam reports whether a template param holds a secret.
func isSensitiveParam(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "password") || strings.Contains(k, "secret")
}

// encodeParams serializes params for storage, encrypted at rest with the
// configured PSK (same mechanism as tenant user passwords).
func encodeParams(params map[string]interface{}) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return gofaxlib.Encrypt(string(raw), gofaxlib.Config.PSK)
}

// decodeParams reverses encodeParams. It tolerates legacy plaintext JSON rows.
func decodeParams(stored string) (map[string]interface{}, error) {
	raw, err := gofaxlib.Decrypt(stored, gofaxlib.Config.PSK)
	if err != nil {
		raw = stored // legacy plaintext row
	}
	params := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("decode gateway params: %w", err)
	}
	return params, nil
}

// maskedParamsJSON returns the params serialized with secret values masked,
// suitable for API responses.
func maskedParamsJSON(stored string) string {
	params, err := decodeParams(stored)
	if err != nil {
		return "{}"
	}
	for k := range params {
		if isSensitiveParam(k) {
			if s, ok := params[k].(string); ok && s != "" {
				params[k] = maskSentinel
			}
		}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// gatewayDir returns the configured gateway directory or an error when
// provisioning is not enabled.
func gatewayDir() (string, error) {
	dir := strings.TrimSpace(gofaxlib.Config.FreeSwitch.GatewayConfigDir)
	if dir == "" {
		return "", errors.New("gateway provisioning is disabled: set freeswitch.gateway_config_dir in config.json")
	}
	return dir, nil
}

// gatewayProfile returns the configured sofia profile hosting the gateways
// (default "fax").
func gatewayProfile() string {
	if p := strings.TrimSpace(gofaxlib.Config.FreeSwitch.GatewayProfile); p != "" {
		return p
	}
	return "fax"
}

// applyGatewayFileOwner chowns a rendered gateway file when
// freeswitch.gateway_config_chown is configured ("user:group" or "uid:gid").
// Failures are logged but non-fatal.
func applyGatewayFileOwner(log *gofaxlib.LogManager, path string) {
	spec := strings.TrimSpace(gofaxlib.Config.FreeSwitch.GatewayConfigChown)
	if spec == "" {
		return
	}
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		log.SendLog(log.BuildLog("Gateway.Provision", fmt.Sprintf("gateway_config_chown %q invalid (want user:group), skipping chown of %s", spec, path), logrus.WarnLevel, nil))
		return
	}
	uid, err := lookupID(parts[0], true)
	if err != nil {
		log.SendLog(log.BuildLog("Gateway.Provision", fmt.Sprintf("gateway_config_chown user %q: %v", parts[0], err), logrus.WarnLevel, nil))
		return
	}
	gid, err := lookupID(parts[1], false)
	if err != nil {
		log.SendLog(log.BuildLog("Gateway.Provision", fmt.Sprintf("gateway_config_chown group %q: %v", parts[1], err), logrus.WarnLevel, nil))
		return
	}
	if err := os.Chown(path, uid, gid); err != nil {
		log.SendLog(log.BuildLog("Gateway.Provision", fmt.Sprintf("chown %s to %s failed (need root/CAP_CHOWN?): %v", path, spec, err), logrus.WarnLevel, nil))
	}
}

// lookupID resolves a numeric id or a user/group name.
func lookupID(s string, isUser bool) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	if isUser {
		u, err := user.Lookup(s)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(u.Uid)
	}
	g, err := user.LookupGroup(s)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}

// TemplateVariables parses a template body and returns the sorted, deduplicated
// list of referenced variables (without the leading dot), e.g. ["realm", "name"].
func TemplateVariables(body string) ([]string, error) {
	t, err := template.New("gateway").Parse(body)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}
	vars := map[string]bool{}
	var walk func(nodes []parse.Node)
	walk = func(nodes []parse.Node) {
		for _, n := range nodes {
			switch node := n.(type) {
			case *parse.ActionNode:
				walk([]parse.Node{node.Pipe})
			case *parse.PipeNode:
				for _, cmd := range node.Cmds {
					walk([]parse.Node{cmd})
				}
			case *parse.CommandNode:
				walk(node.Args)
			case *parse.FieldNode:
				if len(node.Ident) > 0 {
					vars[node.Ident[0]] = true
				}
			case *parse.IfNode:
				walk([]parse.Node{node.Pipe})
				walk(node.List.Nodes)
				if node.ElseList != nil {
					walk(node.ElseList.Nodes)
				}
			case *parse.RangeNode:
				walk([]parse.Node{node.Pipe})
				walk(node.List.Nodes)
				if node.ElseList != nil {
					walk(node.ElseList.Nodes)
				}
			case *parse.WithNode:
				walk([]parse.Node{node.Pipe})
				walk(node.List.Nodes)
				if node.ElseList != nil {
					walk(node.ElseList.Nodes)
				}
			case *parse.TemplateNode:
				// nested template references are not supported; ignore
			case *parse.ListNode:
				walk(node.Nodes)
			}
		}
	}
	for _, tree := range t.Templates() {
		if tree.Root != nil {
			walk(tree.Root.Nodes)
		}
	}
	out := make([]string, 0, len(vars))
	for v := range vars {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

// xmlEscapeParams XML-escapes string parameter values so a malicious or
// careless value (e.g. a realm containing `"/>`) cannot inject markup into
// the rendered gateway XML.
func xmlEscapeParams(params map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(params))
	for k, v := range params {
		if s, ok := v.(string); ok {
			var buf bytes.Buffer
			_ = xml.EscapeText(&buf, []byte(s))
			out[k] = buf.String()
			continue
		}
		out[k] = v
	}
	return out
}

// renderGatewayTemplate renders a template body against params and validates
// that the result is well-formed XML. Variables declared in the template but
// absent from params are injected as empty strings so optional conditionals
// ({{if .username}}...) work; genuinely required values (e.g. realm) are
// validated by the caller.
func renderGatewayTemplate(body string, params map[string]interface{}) (string, error) {
	vars, err := TemplateVariables(body)
	if err != nil {
		return "", err
	}
	escaped := xmlEscapeParams(params)
	p := make(map[string]interface{}, len(escaped)+len(vars))
	for _, v := range vars {
		p[v] = ""
	}
	for k, v := range escaped {
		p[k] = v
	}
	t, err := template.New("gateway").Option("missingkey=error").Parse(body)
	if err != nil {
		return "", fmt.Errorf("invalid template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("template render failed: %w", err)
	}
	out := buf.String()
	if err := xml.Unmarshal([]byte(out), new(interface{})); err != nil {
		return "", fmt.Errorf("rendered gateway XML is not well-formed: %w", err)
	}
	return out, nil
}

// seedGatewayTemplates inserts the embedded default templates when the table
// is empty. Existing templates are never overwritten.
func (s *Server) seedGatewayTemplates() error {
	var count int64
	if err := s.DB.Model(&GatewayTemplate{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	for _, def := range defaultGatewayTemplates {
		tpl := GatewayTemplate{Name: def.name, Description: def.description, Body: def.body}
		if err := s.DB.Create(&tpl).Error; err != nil {
			return fmt.Errorf("seed gateway template %q: %w", def.name, err)
		}
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			fmt.Sprintf("seeded gateway template %q", def.name),
			logrus.InfoLevel,
			nil,
		))
	}
	return nil
}

// ---------------------------------------------------------------------------
// FreeSWITCH control (ESL)
// ---------------------------------------------------------------------------

// fsAPI runs a single `api` command against FreeSWITCH over a short-lived
// event socket connection and returns the trimmed reply body.
func fsAPI(cmd string) (string, error) {
	conn, err := eventsocket.Dial(
		gofaxlib.Config.FreeSwitch.EventClientSocket,
		gofaxlib.Config.FreeSwitch.EventClientSocketPassword,
	)
	if err != nil {
		return "", fmt.Errorf("connect to freeswitch: %w", err)
	}
	defer conn.Close()
	ev, err := conn.Send("api " + cmd)
	if err != nil {
		return "", fmt.Errorf("%q: %w", cmd, err)
	}
	return strings.TrimSpace(ev.Body), nil
}

// fsReloadGateways makes FreeSWITCH pick up gateway file changes on the configured profile.
func fsReloadGateways() error {
	if _, err := fsAPI("reloadxml"); err != nil {
		return err
	}
	_, err := fsAPI("sofia profile " + gatewayProfile() + " rescan")
	return err
}

// fsKillGateway tears down a single gateway and rescans the profile.
func fsKillGateway(name string) error {
	// killgw fails when the gateway was never loaded; that is fine.
	_, _ = fsAPI("sofia killgw " + gatewayProfile() + " " + name)
	return fsReloadGateways()
}

// fsRestartProfile restarts the configured sofia profile. This drops all
// active calls on the profile — callers must require explicit confirmation.
func fsRestartProfile() error {
	_, err := fsAPI("sofia profile " + gatewayProfile() + " restart")
	return err
}

// fsGatewayState best-effort parses `sofia status gateway <name>` output.
func fsGatewayState(name string) string {
	out, err := fsAPI("sofia status gateway " + name)
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "State") {
			return fields[1]
		}
	}
	if strings.Contains(out, "Invalid Gateway") || strings.Contains(out, "-ERR") {
		return "not-loaded"
	}
	return "unknown"
}

// gatewayManagingEndpoint returns the name of the provisioned gateway that
// owns the given endpoint, if any. Such endpoints must be managed through the
// gateway provisioning API, not edited directly.
func (s *Server) gatewayManagingEndpoint(endpointID uint) (string, bool) {
	var gw GatewayConfig
	if err := s.DB.Where("endpoint_id = ?", endpointID).First(&gw).Error; err != nil {
		return "", false
	}
	return gw.Name, true
}

// ---------------------------------------------------------------------------
// Gateway alias endpoints
//
// An alias is a plain number-scoped Endpoint row whose value points at an
// already-provisioned gateway ("name:ip"). Aliases let a number fan out to a
// gateway that already exists on its tenant (or elsewhere) — e.g. portal
// delivery AND PBX forwarding — without re-provisioning the gateway. The
// gateway's linked endpoint stays the only *managed* row; aliases are
// unmanaged (deletable, re-scopable) but their value is gateway-derived:
// UpdateGateway rewrites it on realm/IP changes, direct value edits are
// refused by handleUpdateEndpoint, and DeprovisionGateway refuses to run
// while aliases exist.
// ---------------------------------------------------------------------------

// isGatewayAliasValue reports whether an endpoint value routes onto the named
// gateway: exactly "name" or "name:..." (the endpointValue format). Prefix
// matching on "name:" avoids false positives from names that merely share a
// prefix ("pbx" must not match "pbx_acme:10.0.0.1").
func isGatewayAliasValue(gwName, value string) bool {
	return value == gwName || strings.HasPrefix(value, gwName+":")
}

// gatewayAliasEndpoints returns the endpoints routing onto the given gateway
// other than its linked (managed) endpoint.
func (s *Server) gatewayAliasEndpoints(gw *GatewayConfig) ([]Endpoint, error) {
	var eps []Endpoint
	if err := s.DB.Where("endpoint_type = ?", "gateway").Find(&eps).Error; err != nil {
		return nil, err
	}
	out := make([]Endpoint, 0, len(eps))
	for _, ep := range eps {
		if ep.ID == gw.EndpointID {
			continue
		}
		if isGatewayAliasValue(gw.Name, ep.Endpoint) {
			out = append(out, ep)
		}
	}
	return out, nil
}

// gatewayEndpointValue derives the current "name:ip" value for a gateway —
// from the linked endpoint when present (it tracks realm/endpoint_ip changes
// via UpdateGateway), otherwise from the stored params' realm.
func (s *Server) gatewayEndpointValue(gw *GatewayConfig) (string, error) {
	if gw.EndpointID != 0 {
		var ep Endpoint
		if err := s.DB.First(&ep, gw.EndpointID).Error; err == nil && ep.Endpoint != "" {
			return ep.Endpoint, nil
		}
	}
	params, err := decodeParams(gw.Params)
	if err != nil {
		return "", fmt.Errorf("decode gateway params: %w", err)
	}
	return endpointValue(gw.Name, "", params), nil
}

// CreateGatewayAlias creates a number-scoped endpoint routing onto an
// already-provisioned gateway. No FreeSWITCH interaction is needed: the
// gateway XML, ACL and dialstring target already exist.
func (s *Server) CreateGatewayAlias(name string, spec GatewayAliasSpec) (*Endpoint, error) {
	if err := validateGatewayName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGatewayAliasInvalid, err)
	}
	var gw GatewayConfig
	if err := s.DB.Where("name = ?", name).First(&gw).Error; err != nil {
		return nil, fmt.Errorf("%w: %q", ErrGatewayNotFound, name)
	}

	// The target number must exist — both in the DB and in the loaded
	// TenantNumbers map (addEndpointToDB resolves the number string from the
	// in-memory map when placing the endpoint).
	var tn TenantNumber
	if err := s.DB.First(&tn, spec.TypeID).Error; err != nil {
		return nil, fmt.Errorf("%w: tenant number id %d not found", ErrGatewayAliasInvalid, spec.TypeID)
	}
	s.mu.RLock()
	_, loaded := s.TenantNumbers[tn.Number]
	s.mu.RUnlock()
	if !loaded {
		return nil, fmt.Errorf("%w: tenant number id %d (%s) is not loaded; reload data first", ErrGatewayAliasInvalid, spec.TypeID, tn.Number)
	}

	value, err := s.gatewayEndpointValue(&gw)
	if err != nil {
		return nil, err
	}

	// Refuse exact duplicates: same number already routing to this gateway.
	var dupes int64
	s.DB.Model(&Endpoint{}).Where(
		"type = ? AND type_id = ? AND endpoint_type = ? AND endpoint = ?",
		"number", spec.TypeID, "gateway", value,
	).Count(&dupes)
	if dupes > 0 {
		return nil, fmt.Errorf("%w: number %s already routes to gateway %q", ErrGatewayAliasExists, tn.Number, name)
	}

	ep := &Endpoint{
		Type:         "number",
		TypeID:       spec.TypeID,
		EndpointType: "gateway",
		Endpoint:     value,
		Priority:     spec.Priority,
		Bridge:       spec.Bridge,
	}
	if err := s.addEndpointToDB(ep); err != nil {
		return nil, fmt.Errorf("persist alias endpoint: %w", err)
	}
	// Reload so the in-memory ACL and endpoint maps pick up the new row
	// (addEndpointToDB updates the scope maps but not GatewayEndpointsACL).
	if err := s.loadEndpoints(); err != nil {
		return nil, fmt.Errorf("alias created but endpoint reload failed: %w", err)
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("created alias endpoint %d: number %s routes to gateway %s", ep.ID, tn.Number, name),
		logrus.InfoLevel,
		map[string]interface{}{
			"gateway":     name,
			"endpoint_id": ep.ID,
			"number":      tn.Number,
			"priority":    spec.Priority,
			"bridge":      spec.Bridge,
		},
	))
	return ep, nil
}

// ---------------------------------------------------------------------------
// Provisioning
// ---------------------------------------------------------------------------

func validateGatewayName(name string) error {
	if !gatewayNameRe.MatchString(name) {
		return fmt.Errorf("invalid gateway name %q: must match %s (e.g. pbx_acme)", name, gatewayNameRe.String())
	}
	return nil
}

// validateSpec checks the required fields of a provisioning spec. realm is
// mandatory: without it the rendered sofia gateway cannot identify the peer.
func validateSpec(spec GatewayProvisionSpec) error {
	if err := validateGatewayName(spec.Name); err != nil {
		return err
	}
	realm, _ := spec.Params["realm"].(string)
	if strings.TrimSpace(realm) == "" {
		return errors.New("params.realm is required (IP, hostname or FQDN of the SIP peer)")
	}
	if r, ok := spec.Params["register"].(bool); ok && r {
		u, _ := spec.Params["username"].(string)
		p, _ := spec.Params["password"].(string)
		if u == "" || p == "" {
			return errors.New("params.username and params.password are required when register=true")
		}
	}
	return nil
}

// provisionDefaults fills optional params with sane defaults.
func provisionDefaults(params map[string]interface{}) map[string]interface{} {
	p := make(map[string]interface{}, len(params)+2)
	for k, v := range params {
		p[k] = v
	}
	if _, ok := p["extension"]; !ok || p["extension"] == "" {
		p["extension"] = "auto_to_user"
	}
	if _, ok := p["register"]; !ok {
		p["register"] = false
	}
	return p
}

// writeGatewayFile atomically writes the rendered XML to the gateway directory.
func writeGatewayFile(dir, name, content string) (string, error) {
	path := filepath.Join(dir, name+".xml")
	tmp, err := os.CreateTemp(dir, "."+name+"-*.xml.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after rename
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("rename into place: %w", err)
	}
	return path, nil
}

// endpointValue builds the endpoint string for a gateway ("name:ip", or just
// "name" when no IP is known).
func endpointValue(name, endpointIP string, params map[string]interface{}) string {
	ip := endpointIP
	if ip == "" {
		if realm, ok := params["realm"].(string); ok {
			ip = realm
		}
	}
	if ip != "" {
		return name + ":" + ip
	}
	return name
}

// endpointFromSpec builds the linked Endpoint row for a gateway.
func endpointFromSpec(spec GatewayProvisionSpec) (*Endpoint, error) {
	switch spec.Scope {
	case "tenant", "number", "global":
	default:
		return nil, fmt.Errorf("type must be tenant, number or global, got %q", spec.Scope)
	}
	ep := &Endpoint{
		Type:         spec.Scope,
		TypeID:       spec.TypeID,
		EndpointType: "gateway",
		Endpoint:     endpointValue(spec.Name, spec.EndpointIP, spec.Params),
		Priority:     spec.Priority,
		Bridge:       spec.Bridge,
	}
	if spec.Scope == "global" {
		ep.TypeID = 0
	}
	return ep, nil
}

// ProvisionGateway renders the template, writes the gateway XML, reloads
// FreeSWITCH, and creates the linked endpoint — all in one call.
func (s *Server) ProvisionGateway(spec GatewayProvisionSpec) (*GatewayStatus, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	dir, err := gatewayDir()
	if err != nil {
		return nil, err
	}

	var existing int64
	s.DB.Model(&GatewayConfig{}).Where("name = ?", spec.Name).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("gateway %q is already provisioned (use PUT to update)", spec.Name)
	}

	var tpl GatewayTemplate
	if err := s.DB.First(&tpl, spec.TemplateID).Error; err != nil {
		return nil, fmt.Errorf("template %d: %w", spec.TemplateID, err)
	}

	params := provisionDefaults(spec.Params)
	params["name"] = spec.Name
	rendered, err := renderGatewayTemplate(tpl.Body, params)
	if err != nil {
		return nil, err
	}

	ep, err := endpointFromSpec(spec)
	if err != nil {
		return nil, err
	}

	// Write the XML first so a FreeSWITCH failure never leaves a DB-only row.
	path, err := writeGatewayFile(dir, spec.Name, rendered)
	if err != nil {
		return nil, err
	}
	applyGatewayFileOwner(s.LogManager, path)
	if err := fsReloadGateways(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("wrote %s but FreeSWITCH reload failed (rolled back): %w", path, err)
	}

	paramsJSON, err := encodeParams(params)
	if err != nil {
		os.Remove(path)
		_ = fsKillGateway(spec.Name)
		return nil, fmt.Errorf("encode gateway params: %w", err)
	}
	gw := GatewayConfig{
		Name:       spec.Name,
		TemplateID: tpl.ID,
		Params:     paramsJSON,
	}
	if err := s.addEndpointToDB(ep); err != nil {
		os.Remove(path)
		_ = fsKillGateway(spec.Name)
		return nil, fmt.Errorf("FreeSWITCH gateway loaded but endpoint creation failed (rolled back): %w", err)
	}
	gw.EndpointID = ep.ID
	if err := s.DB.Create(&gw).Error; err != nil {
		_ = s.removeEndpointFromDB(ep.ID)
		os.Remove(path)
		_ = fsKillGateway(spec.Name)
		return nil, fmt.Errorf("persist gateway config: %w", err)
	}
	if err := s.loadEndpoints(); err != nil {
		return nil, fmt.Errorf("gateway provisioned but endpoint reload failed: %w", err)
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("provisioned gateway %s from template %s", spec.Name, tpl.Name),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": spec.Name, "template": tpl.Name, "file": path, "endpoint_id": ep.ID},
	))
	gw.Template = tpl
	gw.Params = maskedParamsJSON(gw.Params)
	return &GatewayStatus{Gateway: gw, File: path, State: fsGatewayState(spec.Name), Exists: true}, nil
}

// UpdateGateway re-renders and rewrites an existing gateway and its endpoint.
func (s *Server) UpdateGateway(name string, spec GatewayProvisionSpec) (*GatewayStatus, error) {
	spec.Name = name
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	dir, err := gatewayDir()
	if err != nil {
		return nil, err
	}

	var gw GatewayConfig
	if err := s.DB.Preload("Template").Where("name = ?", name).First(&gw).Error; err != nil {
		return nil, fmt.Errorf("gateway %q: %w", name, err)
	}

	tpl := gw.Template
	if spec.TemplateID != 0 && spec.TemplateID != gw.TemplateID {
		if err := s.DB.First(&tpl, spec.TemplateID).Error; err != nil {
			return nil, fmt.Errorf("template %d: %w", spec.TemplateID, err)
		}
	}

	params := provisionDefaults(spec.Params)
	// Secrets left as the mask sentinel keep their previously stored value.
	if stored, err := decodeParams(gw.Params); err == nil {
		for k, v := range params {
			if isSensitiveParam(k) && v == maskSentinel {
				if old, ok := stored[k]; ok {
					params[k] = old
				}
			}
		}
	}
	params["name"] = name
	rendered, err := renderGatewayTemplate(tpl.Body, params)
	if err != nil {
		return nil, err
	}

	spec.Name = name

	path, err := writeGatewayFile(dir, name, rendered)
	if err != nil {
		return nil, err
	}
	applyGatewayFileOwner(s.LogManager, path)
	if err := fsReloadGateways(); err != nil {
		return nil, fmt.Errorf("updated %s but FreeSWITCH reload failed: %w", path, err)
	}

	// Endpoint handling on update: preserve the linked endpoint's scope,
	// priority and bridge flag — only the name:ip value may change (e.g. the
	// peer moved). Adopted gateways (endpoint_id 0) never get an endpoint
	// created here; provisioning is the only path that creates endpoints.
	newValue := endpointValue(name, spec.EndpointIP, params)
	if gw.EndpointID != 0 {
		var ep Endpoint
		if err := s.DB.First(&ep, gw.EndpointID).Error; err != nil {
			return nil, fmt.Errorf("gateway file updated but linked endpoint %d read failed: %w", gw.EndpointID, err)
		}
		ep.Endpoint = newValue
		if err := s.updateEndpoint(&ep); err != nil {
			return nil, fmt.Errorf("gateway file updated but endpoint update failed: %w", err)
		}
	}
	// Propagate the value change to alias endpoints (number-scoped endpoints
	// routing onto this gateway) so a peer move does not strand them. Their
	// scope/priority/bridge are untouched; the loadEndpoints below refreshes
	// the in-memory maps and ACL.
	aliases, err := s.gatewayAliasEndpoints(&gw)
	if err != nil {
		return nil, fmt.Errorf("gateway file updated but alias lookup failed: %w", err)
	}
	for _, al := range aliases {
		if al.Endpoint == newValue {
			continue
		}
		al.Endpoint = newValue
		if err := s.DB.Save(&al).Error; err != nil {
			return nil, fmt.Errorf("gateway file updated but alias endpoint %d update failed: %w", al.ID, err)
		}
	}
	paramsJSON, err := encodeParams(params)
	if err != nil {
		return nil, fmt.Errorf("encode gateway params: %w", err)
	}
	gw.TemplateID = tpl.ID
	gw.Params = paramsJSON
	if err := s.DB.Save(&gw).Error; err != nil {
		return nil, fmt.Errorf("persist gateway config: %w", err)
	}
	// Reload endpoints so a changed realm/endpoint_ip refreshes the in-memory
	// GatewayEndpointsACL (and its DNS resolution) without a restart.
	if err := s.loadEndpoints(); err != nil {
		return nil, fmt.Errorf("gateway updated but endpoint reload failed: %w", err)
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("updated gateway %s", name),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": name, "template": tpl.Name, "file": path},
	))
	gw.Template = tpl
	gw.Params = maskedParamsJSON(gw.Params)
	return &GatewayStatus{Gateway: gw, File: path, State: fsGatewayState(name), Exists: true}, nil
}

// DeprovisionGateway removes the gateway XML and tears it down in FreeSWITCH.
// It refuses (ErrGatewayHasAliases) while alias endpoints reference the
// gateway. The linked endpoint row is RETAINED (decoupled, becomes
// unmanaged): routing config survives deprovisioning so the gateway can later
// be reprovisioned onto the same endpoint via ProvisionGatewayFromEndpoint.
// Until then the endpoint dangles — deliveries routed to it fail and its
// "name:ip" ACL entry stays. Delete it manually if the gateway is gone for
// good.
func (s *Server) DeprovisionGateway(name string) error {
	if err := validateGatewayName(name); err != nil {
		return err
	}
	dir, err := gatewayDir()
	if err != nil {
		return err
	}

	var gw GatewayConfig
	if err := s.DB.Where("name = ?", name).First(&gw).Error; err != nil {
		return fmt.Errorf("gateway %q: %w", name, err)
	}

	aliases, err := s.gatewayAliasEndpoints(&gw)
	if err != nil {
		return fmt.Errorf("alias lookup for gateway %q: %w", name, err)
	}
	if len(aliases) > 0 {
		ids := make([]uint, 0, len(aliases))
		for _, al := range aliases {
			ids = append(ids, al.ID)
		}
		return fmt.Errorf("%w: gateway %q has %d alias endpoint(s) (ids %v); remove them before deprovisioning",
			ErrGatewayHasAliases, name, len(aliases), ids)
	}

	_ = os.Remove(filepath.Join(dir, name+".xml"))
	if err := fsKillGateway(name); err != nil {
		return fmt.Errorf("file removed but FreeSWITCH reload failed: %w", err)
	}
	if gw.EndpointID != 0 {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Gateway.Provision",
			fmt.Sprintf("gateway %s deprovisioned; linked endpoint %d retained (now unmanaged, routes will fail until reprovisioned or deleted)", name, gw.EndpointID),
			logrus.WarnLevel,
			map[string]interface{}{"gateway": name, "endpoint_id": gw.EndpointID},
		))
	}
	if err := s.DB.Delete(&gw).Error; err != nil {
		return fmt.Errorf("delete gateway config: %w", err)
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("deprovisioned gateway %s", name),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": name},
	))
	return nil
}

// ProvisionGatewayFromEndpointSpec is the payload for bringing an existing
// (e.g. imported) endpoint under gateway management: the endpoint's value
// ("name:ip") supplies the gateway name and default realm, the chosen
// template is rendered and written, and a GatewayConfig row is linked to the
// existing endpoint (no new endpoint is created).
type ProvisionGatewayFromEndpointSpec struct {
	EndpointID uint                   `json:"endpoint_id"`
	TemplateID uint                   `json:"template_id"`
	Params     map[string]interface{} `json:"params"`
}

// ProvisionGatewayFromEndpoint renders a gateway XML for an existing
// unmanaged endpoint and records the GatewayConfig linkage.
func (s *Server) ProvisionGatewayFromEndpoint(spec ProvisionGatewayFromEndpointSpec) (*GatewayStatus, error) {
	dir, err := gatewayDir()
	if err != nil {
		return nil, err
	}

	var ep Endpoint
	if err := s.DB.First(&ep, spec.EndpointID).Error; err != nil {
		return nil, fmt.Errorf("endpoint %d: %w", spec.EndpointID, err)
	}
	if ep.EndpointType != "gateway" {
		return nil, fmt.Errorf("endpoint %d is type %q, not gateway", ep.ID, ep.EndpointType)
	}
	if gwName, managed := s.gatewayManagingEndpoint(ep.ID); managed {
		return nil, fmt.Errorf("endpoint %d is already managed by gateway %q", ep.ID, gwName)
	}

	// Endpoint value is "name" or "name:ip" (see endpointValue).
	name, ip, _ := strings.Cut(ep.Endpoint, ":")
	if err := validateGatewayName(name); err != nil {
		return nil, fmt.Errorf("endpoint %d value %q does not start with a usable gateway name: %w", ep.ID, ep.Endpoint, err)
	}
	var existing int64
	s.DB.Model(&GatewayConfig{}).Where("name = ?", name).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("gateway %q is already provisioned", name)
	}

	var tpl GatewayTemplate
	if err := s.DB.First(&tpl, spec.TemplateID).Error; err != nil {
		return nil, fmt.Errorf("template %d: %w", spec.TemplateID, err)
	}

	params := provisionDefaults(spec.Params)
	if realm, _ := params["realm"].(string); strings.TrimSpace(realm) == "" {
		if ip == "" {
			return nil, fmt.Errorf("endpoint %d value %q has no IP; params.realm is required", ep.ID, ep.Endpoint)
		}
		params["realm"] = ip
	}
	params["name"] = name
	rendered, err := renderGatewayTemplate(tpl.Body, params)
	if err != nil {
		return nil, err
	}

	// Write the XML first so a FreeSWITCH failure never leaves a DB-only row.
	path, err := writeGatewayFile(dir, name, rendered)
	if err != nil {
		return nil, err
	}
	applyGatewayFileOwner(s.LogManager, path)
	if err := fsReloadGateways(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("wrote %s but FreeSWITCH reload failed (rolled back): %w", path, err)
	}

	paramsJSON, err := encodeParams(params)
	if err != nil {
		os.Remove(path)
		_ = fsKillGateway(name)
		return nil, fmt.Errorf("encode gateway params: %w", err)
	}
	gw := GatewayConfig{
		Name:       name,
		TemplateID: tpl.ID,
		Params:     paramsJSON,
		EndpointID: ep.ID,
	}
	if err := s.DB.Create(&gw).Error; err != nil {
		os.Remove(path)
		_ = fsKillGateway(name)
		return nil, fmt.Errorf("persist gateway config: %w", err)
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("provisioned gateway %s from existing endpoint %d", name, ep.ID),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": name, "template": tpl.Name, "file": path, "endpoint_id": ep.ID},
	))
	gw.Template = tpl
	gw.Params = maskedParamsJSON(gw.Params)
	return &GatewayStatus{Gateway: gw, File: path, State: fsGatewayState(name), Exists: true}, nil
}

// GatewayOverview is the full DB + disk picture for the gateways directory.
type GatewayOverview struct {
	Gateways  []GatewayStatus `json:"gateways"`
	Unmanaged []UnmanagedFile `json:"unmanaged"` // XML files on disk with no DB row
}

// UnmanagedFile is a gateway XML present in the gateway directory that no
// GatewayConfig row tracks (manually created, or left behind).
type UnmanagedFile struct {
	File string `json:"file"` // base filename, e.g. pbx_acme.xml
	Name string `json:"name"` // parsed <gateway name=...> attribute, empty if unparseable
}

// gatewayNameFromFile extracts the gateway name attribute from an XML file.
func gatewayNameFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc struct {
		Gateways []struct {
			Name string `xml:"name,attr"`
		} `xml:"gateway"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if len(doc.Gateways) == 0 || doc.Gateways[0].Name == "" {
		return "", fmt.Errorf("no <gateway name=...> found in %s", filepath.Base(path))
	}
	return doc.Gateways[0].Name, nil
}

// ListGateways returns the DB-tracked gateways plus unmanaged files on disk.
func (s *Server) ListGateways() (*GatewayOverview, error) {
	var gws []GatewayConfig
	if err := s.DB.Preload("Template").Find(&gws).Error; err != nil {
		return nil, err
	}
	dir, dirErr := gatewayDir()
	overview := &GatewayOverview{Gateways: make([]GatewayStatus, 0, len(gws)), Unmanaged: []UnmanagedFile{}}
	tracked := map[string]bool{}
	for _, gw := range gws {
		tracked[gw.Name] = true
		gw.Params = maskedParamsJSON(gw.Params)
		gs := GatewayStatus{Gateway: gw, State: fsGatewayState(gw.Name)}
		if aliases, err := s.gatewayAliasEndpoints(&gw); err == nil && len(aliases) > 0 {
			gs.Aliases = aliases
		}
		if dirErr == nil {
			gs.File = filepath.Join(dir, gw.Name+".xml")
			if _, err := os.Stat(gs.File); err == nil {
				gs.Exists = true
			}
		}
		overview.Gateways = append(overview.Gateways, gs)
	}
	if dirErr == nil {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".xml") || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				base := strings.TrimSuffix(e.Name(), ".xml")
				if tracked[base] {
					continue
				}
				name, _ := gatewayNameFromFile(filepath.Join(dir, e.Name()))
				overview.Unmanaged = append(overview.Unmanaged, UnmanagedFile{File: e.Name(), Name: name})
			}
		}
	}
	return overview, nil
}

// AdoptGateway brings an unmanaged XML file under management: parses the
// gateway name and records a GatewayConfig row (no template — the file stays
// as-is until a template is chosen on update).
func (s *Server) AdoptGateway(file string) (*GatewayStatus, error) {
	dir, err := gatewayDir()
	if err != nil {
		return nil, err
	}
	base := filepath.Base(file) // strip any path traversal attempt
	if !strings.HasSuffix(base, ".xml") {
		return nil, fmt.Errorf("not an XML file: %q", base)
	}
	name, err := gatewayNameFromFile(filepath.Join(dir, base))
	if err != nil {
		return nil, err
	}
	if err := validateGatewayName(name); err != nil {
		return nil, fmt.Errorf("file %s declares an unusable gateway name: %w", base, err)
	}
	var existing int64
	s.DB.Model(&GatewayConfig{}).Where("name = ?", name).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("gateway %q is already managed", name)
	}
	gw := GatewayConfig{Name: name, TemplateID: 0}
	paramsJSON, err := encodeParams(map[string]interface{}{"name": name})
	if err != nil {
		return nil, err
	}
	gw.Params = paramsJSON
	if err := s.DB.Create(&gw).Error; err != nil {
		return nil, fmt.Errorf("persist gateway config: %w", err)
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("adopted unmanaged gateway file %s as %s", base, name),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": name, "file": base},
	))
	return &GatewayStatus{Gateway: gw, File: filepath.Join(dir, base), State: fsGatewayState(name), Exists: true}, nil
}

// DeleteUnmanagedGateway removes an untracked XML file and tears the gateway
// down in FreeSWITCH.
func (s *Server) DeleteUnmanagedGateway(name string) error {
	if err := validateGatewayName(name); err != nil {
		return err
	}
	dir, err := gatewayDir()
	if err != nil {
		return err
	}
	var tracked int64
	s.DB.Model(&GatewayConfig{}).Where("name = ?", name).Count(&tracked)
	if tracked > 0 {
		return fmt.Errorf("gateway %q is managed; use DELETE /admin/gateway/%s", name, name)
	}
	path := filepath.Join(dir, name+".xml")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no unmanaged file for %q: %w", name, err)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return fsKillGateway(name)
}

// RepairGateway re-renders a managed gateway from its stored template and
// params — restoring a file that was deleted or edited out-of-band.
func (s *Server) RepairGateway(name string) (*GatewayStatus, error) {
	if err := validateGatewayName(name); err != nil {
		return nil, err
	}
	dir, err := gatewayDir()
	if err != nil {
		return nil, err
	}
	var gw GatewayConfig
	if err := s.DB.Preload("Template").Where("name = ?", name).First(&gw).Error; err != nil {
		return nil, fmt.Errorf("gateway %q: %w", name, err)
	}
	if gw.TemplateID == 0 {
		return nil, fmt.Errorf("gateway %q has no template (adopted from file); assign a template via PUT /admin/gateway/%s first", name, name)
	}
	params, err := decodeParams(gw.Params)
	if err != nil {
		return nil, err
	}
	params["name"] = name
	rendered, err := renderGatewayTemplate(gw.Template.Body, params)
	if err != nil {
		return nil, err
	}
	path, err := writeGatewayFile(dir, name, rendered)
	if err != nil {
		return nil, err
	}
	applyGatewayFileOwner(s.LogManager, path)
	if err := fsReloadGateways(); err != nil {
		return nil, fmt.Errorf("rewrote %s but FreeSWITCH reload failed: %w", path, err)
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Provision",
		fmt.Sprintf("re-rendered gateway %s from template", name),
		logrus.InfoLevel,
		map[string]interface{}{"gateway": name, "file": path},
	))
	gw.Params = maskedParamsJSON(gw.Params)
	return &GatewayStatus{Gateway: gw, File: path, State: fsGatewayState(name), Exists: true}, nil
}
