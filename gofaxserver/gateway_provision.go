package gofaxserver

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// characters (e.g. "pbx_acme", "sbc_easybell").
var gatewayNameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

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
	Params     string          `json:"params"` // JSON object of template variables
	EndpointID uint            `json:"endpoint_id"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
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

// fsReloadGateways makes FreeSWITCH pick up gateway file changes on the fax profile.
func fsReloadGateways() error {
	if _, err := fsAPI("reloadxml"); err != nil {
		return err
	}
	_, err := fsAPI("sofia profile fax rescan")
	return err
}

// fsKillGateway tears down a single gateway and rescans the profile.
func fsKillGateway(name string) error {
	// killgw fails when the gateway was never loaded; that is fine.
	_, _ = fsAPI("sofia killgw fax " + name)
	return fsReloadGateways()
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

// endpointFromSpec builds the linked Endpoint row for a gateway.
func endpointFromSpec(spec GatewayProvisionSpec) (*Endpoint, error) {
	switch spec.Scope {
	case "tenant", "number", "global":
	default:
		return nil, fmt.Errorf("type must be tenant, number or global, got %q", spec.Scope)
	}
	ip := spec.EndpointIP
	if ip == "" {
		if realm, ok := spec.Params["realm"].(string); ok {
			ip = realm
		}
	}
	ep := &Endpoint{
		Type:         spec.Scope,
		TypeID:       spec.TypeID,
		EndpointType: "gateway",
		Priority:     spec.Priority,
		Bridge:       spec.Bridge,
	}
	if spec.Scope == "global" {
		ep.TypeID = 0
	}
	if ip != "" {
		ep.Endpoint = spec.Name + ":" + ip
	} else {
		ep.Endpoint = spec.Name
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
	ep, err := endpointFromSpec(spec)
	if err != nil {
		return nil, err
	}
	ep.ID = gw.EndpointID

	path, err := writeGatewayFile(dir, name, rendered)
	if err != nil {
		return nil, err
	}
	if err := fsReloadGateways(); err != nil {
		return nil, fmt.Errorf("updated %s but FreeSWITCH reload failed: %w", path, err)
	}

	if err := s.updateEndpoint(ep); err != nil {
		return nil, fmt.Errorf("gateway file updated but endpoint update failed: %w", err)
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

// DeprovisionGateway removes the gateway XML, tears it down in FreeSWITCH,
// and deletes the linked endpoint row.
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

	_ = os.Remove(filepath.Join(dir, name+".xml"))
	if err := fsKillGateway(name); err != nil {
		return fmt.Errorf("file removed but FreeSWITCH reload failed: %w", err)
	}
	if gw.EndpointID != 0 {
		if err := s.removeEndpointFromDB(gw.EndpointID); err != nil {
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"Gateway.Provision",
				fmt.Sprintf("gateway %s removed but endpoint %d delete failed: %v", name, gw.EndpointID, err),
				logrus.WarnLevel,
				nil,
			))
		}
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

// ListGateways returns all provisioned gateways with their live sofia state.
func (s *Server) ListGateways() ([]GatewayStatus, error) {
	var gws []GatewayConfig
	if err := s.DB.Preload("Template").Find(&gws).Error; err != nil {
		return nil, err
	}
	dir, dirErr := gatewayDir()
	out := make([]GatewayStatus, 0, len(gws))
	for _, gw := range gws {
		gw.Params = maskedParamsJSON(gw.Params)
		gs := GatewayStatus{Gateway: gw, State: fsGatewayState(gw.Name)}
		if dirErr == nil {
			gs.File = filepath.Join(dir, gw.Name+".xml")
			if _, err := os.Stat(gs.File); err == nil {
				gs.Exists = true
			}
		}
		out = append(out, gs)
	}
	return out, nil
}
