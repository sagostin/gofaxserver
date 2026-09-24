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

package fsclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin typed wrapper over the gofaxserver HTTP API. It holds no
// state beyond configuration; credentials are supplied per call so admin-key
// calls and per-org service-account calls never mix.
type Client struct {
	BaseURL  string
	AdminKey string
	HTTP     *http.Client
}

func New(baseURL, adminKey string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		AdminKey: adminKey,
		HTTP:     &http.Client{Timeout: 120 * time.Second}, // generous: upstream PDF→TIFF can be slow
	}
}

// APIError preserves the upstream status code for mapping to portal responses.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("gofaxserver %d: %s", e.Status, e.Message) }

// --- request helpers ---

func (c *Client) do(method, path, basicUser, basicPass string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if basicPass != "" || basicUser != "" {
		req.SetBasicAuth(basicUser, basicPass)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("gofaxserver unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) doAdmin(method, path string, payload any, out any) error {
	var body io.Reader
	ct := ""
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
		ct = "application/json"
	}
	return c.do(method, path, "admin", c.AdminKey, body, ct, out)
}

// --- response models (subset of gofaxserver payloads we consume) ---

type TenantNumber struct {
	ID       uint   `json:"id"`
	TenantID uint   `json:"tenant_id"`
	Number   string `json:"number"`
	Name     string `json:"name"`
	Header   string `json:"header"`
	Notify   string `json:"notify"`
}

type Tenant struct {
	ID      uint           `json:"id"`
	Name    string         `json:"name"`
	Notify  string         `json:"notify"`
	Numbers []TenantNumber `json:"numbers"`
}

type TenantUser struct {
	ID       uint   `json:"id"`
	TenantID uint   `json:"tenant_id"`
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
}

type Endpoint struct {
	ID           uint   `json:"id"`
	Type         string `json:"type"`
	TypeID       uint   `json:"type_id"`
	EndpointType string `json:"endpoint_type"`
	Endpoint     string `json:"endpoint"`
	Priority     uint   `json:"priority"`
	Bridge       bool   `json:"bridge"`
}

// GatewayTemplate mirrors gofaxserver's database-backed FS gateway template.
type GatewayTemplate struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	Variables   []string  `json:"variables"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GatewayConfig mirrors gofaxserver's provisioned-gateway record.
type GatewayConfig struct {
	ID                 uint            `json:"id"`
	Name               string          `json:"name"`
	TemplateID         uint            `json:"template_id"`
	Template           GatewayTemplate `json:"template"`
	Params             string          `json:"params"`
	EndpointID         uint            `json:"endpoint_id"`
	LastState          string          `json:"last_state"`
	LastStateCheckedAt *time.Time      `json:"last_state_checked_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// GatewayStatus pairs a provisioned gateway with its live sofia state.
type GatewayStatus struct {
	Gateway GatewayConfig `json:"gateway"`
	File    string        `json:"file"`
	State   string        `json:"state"`
	Exists  bool          `json:"exists_on_disk"`
}

// GatewayProvisionSpec is the combined provision payload (XML + endpoint).
type GatewayProvisionSpec struct {
	Name       string                 `json:"name"`
	TemplateID uint                   `json:"template_id"`
	Params     map[string]interface{} `json:"params"`
	Scope      string                 `json:"type"`
	TypeID     uint                   `json:"type_id"`
	Priority   uint                   `json:"priority"`
	Bridge     bool                   `json:"bridge"`
	EndpointIP string                 `json:"endpoint_ip"`
}

// UnmanagedFile is a gateway XML on disk with no DB row tracking it.
type UnmanagedFile struct {
	File string `json:"file"`
	Name string `json:"name"`
}

// ProvisionGatewayFromEndpointSpec is the payload for bringing an existing
// unmanaged endpoint under gateway management.
type ProvisionGatewayFromEndpointSpec struct {
	EndpointID uint                   `json:"endpoint_id"`
	TemplateID uint                   `json:"template_id"`
	Params     map[string]interface{} `json:"params"`
}

// GatewayOverview mirrors gofaxserver's full DB + disk gateway picture.
type GatewayOverview struct {
	Gateways  []GatewayStatus `json:"gateways"`
	Unmanaged []UnmanagedFile `json:"unmanaged"`
}

// DialplanRule mirrors gofaxserver's DB-backed dialplan rule.
type DialplanRule struct {
	ID          uint   `json:"id"`
	Position    int    `json:"position"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// DialplanView is the rule list plus the active source mode.
type DialplanView struct {
	Source string         `json:"source"`
	Rules  []DialplanRule `json:"rules"`
}

// FaxPolicyRule mirrors gofaxserver's Postgres-backed fax policy rule.
type FaxPolicyRule struct {
	ID            uint       `json:"id"`
	Scope         string     `json:"scope"` // dst | src | pair
	SrcNumber     string     `json:"src_number"`
	DstNumber     string     `json:"dst_number"`
	Effect        string     `json:"effect"`     // t38_off | t38_on | ecm_off | ecm_on | v17_off | softmodem_only | var_override
	AppliesTo     string     `json:"applies_to"` // both | softmodem | bridge
	Origin        string     `json:"origin"`     // manual | auto
	Enabled       bool       `json:"enabled"`
	ExpiresAt     *time.Time `json:"expires_at"`
	VarName       string     `json:"var_name"`  // effect=var_override only
	VarValue      string     `json:"var_value"` // effect=var_override only
	FailureCount  int        `json:"failure_count"`
	SuccessCount  int        `json:"success_count"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	LastT38Status string     `json:"last_t38_status"`
	Notes         string     `json:"notes"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// FaxPolicyList wraps the rule list response.
type FaxPolicyList struct {
	Rules []FaxPolicyRule `json:"rules"`
}

// FaxPolicyResolution mirrors gofaxserver's resolved policy for a call.
type FaxPolicyResolution struct {
	EnableT38      bool   `json:"enable_t38"`
	RequestT38     bool   `json:"request_t38"`
	UseECM         *bool  `json:"use_ecm"`
	DisableV17     *bool  `json:"disable_v17"`
	T38Decided     bool   `json:"t38_decided"`
	T38ForcedOff   bool   `json:"t38_forced_off"`
	SoftmodemOnly  bool   `json:"softmodem_only"`
	AppliedRuleIDs []uint `json:"applied_rule_ids"`
}

// FaxPolicyResolveResult is the dry-run resolve response.
type FaxPolicyResolveResult struct {
	Policy       FaxPolicyResolution `json:"policy"`
	AppliedRules []FaxPolicyRule     `json:"applied_rules"`
}

// FaxPairState mirrors gofaxserver's persisted flip-flop pair state.
type FaxPairState struct {
	ID          uint      `json:"id"`
	SrcNumber   string    `json:"src_number"`
	DstNumber   string    `json:"dst_number"`
	CallType    string    `json:"call_type"`
	LastUsedT38 bool      `json:"last_used_t38"`
	LastSeen    time.Time `json:"last_seen"`
}

// FaxPairStateList wraps the pair-state list response.
type FaxPairStateList struct {
	PairStates []FaxPairState `json:"pair_states"`
}

// FaxRunState mirrors the subset of gofaxserver's FaxRunState we need.
type FaxRunState struct {
	JobUUID    string    `json:"job_uuid"`
	Phase      string    `json:"phase"`
	Caller     string    `json:"caller"`
	Callee     string    `json:"callee"`
	Attempt    int       `json:"attempt"`
	ResultText string    `json:"last_result"`
	Success    bool      `json:"result_success"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ActiveFaxes struct {
	Active int           `json:"active"`
	Items  []FaxRunState `json:"items"`
}

// FaxStatusRow mirrors one fax_job_results row.
type FaxStatusRow struct {
	JobUUID          string    `json:"job_uuid"`
	ResultType       string    `json:"result_type"`
	AttemptNumber    int       `json:"attempt_number"`
	StartTs          time.Time `json:"start_ts"`
	EndTs            time.Time `json:"end_ts"`
	HangupCause      string    `json:"hangup_cause"`
	TransferredPages int       `json:"transferred_pages"`
	Success          bool      `json:"success"`
	ResultText       string    `json:"result_text"`
	T38Status        string    `json:"t38_status"`
}

// --- admin API ---

func (c *Client) ListTenants() ([]Tenant, error) {
	var out []Tenant
	err := c.doAdmin(http.MethodGet, "/admin/tenants", nil, &out)
	return out, err
}

func (c *Client) CreateTenant(name, notify string) (*Tenant, error) {
	out := &Tenant{}
	err := c.doAdmin(http.MethodPost, "/admin/tenant", map[string]string{"name": name, "notify": notify}, out)
	return out, err
}

func (c *Client) UpdateTenant(id uint, name, notify string) error {
	payload := map[string]string{"name": name, "notify": notify}
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/tenant/%d", id), payload, nil)
}

func (c *Client) DeleteTenant(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/tenant/%d", id), nil, nil)
}

func (c *Client) ListNumbers(tenantID uint) ([]TenantNumber, error) {
	path := "/admin/numbers"
	if tenantID > 0 {
		path += fmt.Sprintf("?tenant_id=%d", tenantID)
	}
	var out []TenantNumber
	err := c.doAdmin(http.MethodGet, path, nil, &out)
	return out, err
}

// AddNumber creates the tenant number upstream and returns it (with ID).
func (c *Client) AddNumber(tenantID uint, number, name, header, notify string) (*TenantNumber, error) {
	payload := map[string]any{
		"tenant_id": tenantID,
		"number":    number,
		"name":      name,
		"header":    header,
		"notify":    notify,
	}
	out := &TenantNumber{}
	err := c.doAdmin(http.MethodPost, "/admin/number", payload, out)
	return out, err
}

func (c *Client) UpdateNumber(id uint, tenantID uint, number, name, header, notify string) error {
	payload := map[string]any{
		"tenant_id": tenantID,
		"number":    number,
		"name":      name,
		"header":    header,
		"notify":    notify,
	}
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/number/%d", id), payload, nil)
}

func (c *Client) DeleteNumber(tenantID uint, number string) error {
	path := fmt.Sprintf("/admin/number?number=%s&tenant_id=%d", number, tenantID)
	return c.doAdmin(http.MethodDelete, path, nil, nil)
}

func (c *Client) ListUsers(tenantID uint) ([]TenantUser, error) {
	path := "/admin/users"
	if tenantID > 0 {
		path += fmt.Sprintf("?tenant_id=%d", tenantID)
	}
	var out []TenantUser
	err := c.doAdmin(http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) CreateTenantUser(tenantID uint, username, password, apiKey string) (*TenantUser, error) {
	payload := map[string]any{
		"tenant_id": tenantID,
		"username":  username,
		"password":  password,
		"api_key":   apiKey,
	}
	out := &TenantUser{}
	err := c.doAdmin(http.MethodPost, "/admin/user", payload, out)
	return out, err
}

func (c *Client) DeleteTenantUser(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/user/%d", id), nil, nil)
}

// UpdateTenantUser updates an upstream tenant user. Empty password leaves the
// existing password unchanged (upstream behavior).
func (c *Client) UpdateTenantUser(id, tenantID uint, username, password, apiKey string) error {
	payload := map[string]any{
		"tenant_id": tenantID,
		"username":  username,
		"password":  password,
		"api_key":   apiKey,
	}
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/user/%d", id), payload, nil)
}

func (c *Client) AddEndpoint(ep Endpoint) (*Endpoint, error) {
	out := &Endpoint{}
	err := c.doAdmin(http.MethodPost, "/admin/endpoint", ep, out)
	return out, err
}

func (c *Client) UpdateEndpoint(ep Endpoint) error {
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/endpoint/%d", ep.ID), ep, nil)
}

func (c *Client) DeleteEndpoint(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/endpoint/%d", id), nil, nil)
}

// --- gateway templates & provisioning ---

func (c *Client) ListGatewayTemplates() ([]GatewayTemplate, error) {
	var out []GatewayTemplate
	err := c.doAdmin(http.MethodGet, "/admin/gateway/templates", nil, &out)
	return out, err
}

func (c *Client) CreateGatewayTemplate(tpl GatewayTemplate) (*GatewayTemplate, error) {
	out := &GatewayTemplate{}
	err := c.doAdmin(http.MethodPost, "/admin/gateway/templates", tpl, out)
	return out, err
}

func (c *Client) UpdateGatewayTemplate(tpl GatewayTemplate) error {
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/gateway/templates/%d", tpl.ID), tpl, nil)
}

func (c *Client) DeleteGatewayTemplate(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/gateway/templates/%d", id), nil, nil)
}

func (c *Client) ListGateways() (*GatewayOverview, error) {
	out := &GatewayOverview{}
	err := c.doAdmin(http.MethodGet, "/admin/gateways", nil, out)
	return out, err
}

func (c *Client) AdoptGateway(file string) (*GatewayStatus, error) {
	out := &GatewayStatus{}
	err := c.doAdmin(http.MethodPost, "/admin/gateways/adopt", map[string]string{"file": file}, out)
	return out, err
}

func (c *Client) DeleteUnmanagedGateway(name string) error {
	return c.doAdmin(http.MethodDelete, "/admin/gateways/unmanaged/"+name+"?confirm=true", nil, nil)
}

func (c *Client) RepairGateway(name string) (*GatewayStatus, error) {
	out := &GatewayStatus{}
	err := c.doAdmin(http.MethodPost, "/admin/gateway/"+name+"/repair", nil, out)
	return out, err
}

func (c *Client) ProvisionGatewayFromEndpoint(spec ProvisionGatewayFromEndpointSpec) (*GatewayStatus, error) {
	out := &GatewayStatus{}
	err := c.doAdmin(http.MethodPost, "/admin/gateways/from-endpoint", spec, out)
	return out, err
}

func (c *Client) ProvisionGateway(spec GatewayProvisionSpec) (*GatewayStatus, error) {
	out := &GatewayStatus{}
	err := c.doAdmin(http.MethodPost, "/admin/gateway", spec, out)
	return out, err
}

func (c *Client) UpdateGateway(name string, spec GatewayProvisionSpec) (*GatewayStatus, error) {
	out := &GatewayStatus{}
	err := c.doAdmin(http.MethodPut, "/admin/gateway/"+name, spec, out)
	return out, err
}

func (c *Client) DeprovisionGateway(name string) error {
	return c.doAdmin(http.MethodDelete, "/admin/gateway/"+name, nil, nil)
}

// --- FreeSWITCH profile control ---

func (c *Client) RescanFSProfile() error {
	return c.doAdmin(http.MethodPost, "/admin/freeswitch/rescan", nil, nil)
}

func (c *Client) RestartFSProfile() error {
	return c.doAdmin(http.MethodPost, "/admin/freeswitch/profile/restart?confirm=true", nil, nil)
}

// --- dialplan rules ---

func (c *Client) GetDialplan() (*DialplanView, error) {
	out := &DialplanView{}
	err := c.doAdmin(http.MethodGet, "/admin/dialplan", nil, out)
	return out, err
}

func (c *Client) CreateDialplanRule(r DialplanRule) (*DialplanRule, error) {
	out := &DialplanRule{}
	err := c.doAdmin(http.MethodPost, "/admin/dialplan/rules", r, out)
	return out, err
}

func (c *Client) UpdateDialplanRule(r DialplanRule) error {
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/dialplan/rules/%d", r.ID), r, nil)
}

func (c *Client) DeleteDialplanRule(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/dialplan/rules/%d", id), nil, nil)
}

func (c *Client) ReorderDialplanRules(order []map[string]uint) error {
	return c.doAdmin(http.MethodPost, "/admin/dialplan/rules/reorder", order, nil)
}

// --- fax policy rules ---

func (c *Client) ListFaxPolicies(number, origin string) (*FaxPolicyList, error) {
	qs := []string{}
	if number != "" {
		qs = append(qs, "number="+url.QueryEscape(number))
	}
	if origin != "" {
		qs = append(qs, "origin="+url.QueryEscape(origin))
	}
	path := "/admin/fax-policies"
	if len(qs) > 0 {
		path += "?" + strings.Join(qs, "&")
	}
	out := &FaxPolicyList{}
	err := c.doAdmin(http.MethodGet, path, nil, out)
	return out, err
}

func (c *Client) CreateFaxPolicyRule(r FaxPolicyRule) (*FaxPolicyRule, error) {
	out := &FaxPolicyRule{}
	err := c.doAdmin(http.MethodPost, "/admin/fax-policies", r, out)
	return out, err
}

func (c *Client) UpdateFaxPolicyRule(r FaxPolicyRule) error {
	return c.doAdmin(http.MethodPut, fmt.Sprintf("/admin/fax-policies/%d", r.ID), r, nil)
}

func (c *Client) DeleteFaxPolicyRule(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/fax-policies/%d", id), nil, nil)
}

func (c *Client) ExpireFaxPolicyRule(id uint) error {
	return c.doAdmin(http.MethodPost, fmt.Sprintf("/admin/fax-policies/%d/expire", id), nil, nil)
}

func (c *Client) ResolveFaxPolicy(src, dst, callType string) (*FaxPolicyResolveResult, error) {
	path := fmt.Sprintf("/admin/fax-policies/resolve?src=%s&dst=%s&type=%s",
		url.QueryEscape(src), url.QueryEscape(dst), url.QueryEscape(callType))
	out := &FaxPolicyResolveResult{}
	err := c.doAdmin(http.MethodGet, path, nil, out)
	return out, err
}

func (c *Client) ListFaxPairStates(number string) (*FaxPairStateList, error) {
	path := "/admin/fax-policies/pair-states"
	if number != "" {
		path += "?number=" + url.QueryEscape(number)
	}
	out := &FaxPairStateList{}
	err := c.doAdmin(http.MethodGet, path, nil, out)
	return out, err
}

func (c *Client) DeleteFaxPairState(id uint) error {
	return c.doAdmin(http.MethodDelete, fmt.Sprintf("/admin/fax-policies/pair-states/%d", id), nil, nil)
}

func (c *Client) ListEndpoints(typeFilter string, typeID uint) ([]Endpoint, error) {
	qs := []string{}
	if typeFilter != "" {
		qs = append(qs, "type="+typeFilter)
	}
	if typeID > 0 {
		qs = append(qs, fmt.Sprintf("type_id=%d", typeID))
	}
	path := "/admin/endpoints"
	if len(qs) > 0 {
		path += "?" + strings.Join(qs, "&")
	}
	var out []Endpoint
	err := c.doAdmin(http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) ListActiveFaxes() (*ActiveFaxes, error) {
	out := &ActiveFaxes{}
	err := c.doAdmin(http.MethodGet, "/admin/faxes", nil, out)
	return out, err
}

// --- tenant-user context API (service accounts) ---

// AuthenticateSvcAccount validates svc credentials via the self-service endpoint.
func (c *Client) AuthenticateSvcAccount(username, password string) error {
	return c.do(http.MethodPost, "/tenant/user/authenticate", username, password, nil, "", nil)
}

// SendFax uploads a document on behalf of a service account and returns the job UUID.
func (c *Client) SendFax(username, password, filename string, fileBytes []byte, caller, callee string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(fileBytes); err != nil {
		return "", err
	}
	_ = w.WriteField("caller_number", caller)
	_ = w.WriteField("callee_number", callee)
	if err := w.Close(); err != nil {
		return "", err
	}
	out := map[string]string{}
	if err := c.do(http.MethodPost, "/fax/send", username, password, &buf, w.FormDataContentType(), &out); err != nil {
		return "", err
	}
	uuid := out["job_uuid"]
	if uuid == "" {
		return "", fmt.Errorf("gofaxserver did not return job_uuid")
	}
	return uuid, nil
}

func (c *Client) GetFaxStatus(username, password, uuid string) ([]FaxStatusRow, error) {
	var out []FaxStatusRow
	err := c.do(http.MethodGet, "/fax/status?uuid="+uuid, username, password, nil, "", &out)
	return out, err
}
