package fsclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
