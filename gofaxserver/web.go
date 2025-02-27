package gofaxserver

import (
	"encoding/base64"
	"fmt"
	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// loadWebPaths sets up the HTTP routes for tenant user management.
func (s *Server) loadWebPaths(app *iris.Application) {
	admin := app.Party("/admin", s.basicAuthMiddleware)
	{
		// Tenant management endpoints.
		admin.Post("/tenant", s.handleAddTenant)
		admin.Put("/tenant/{id}", s.handleUpdateTenant)
		admin.Delete("/tenant/{id}", s.handleDeleteTenant)

		// Tenant Numbers management.
		admin.Post("/number", s.handleAddTenantNumber)
		admin.Put("/number/{id}", s.handleUpdateTenantNumber)
		admin.Delete("/number", s.handleDeleteTenantNumber)

		// Endpoints management.
		admin.Post("/endpoint", s.handleAddEndpoint)
		admin.Put("/endpoint/{id}", s.handleUpdateEndpoint)
		admin.Delete("/endpoint/{id}", s.handleDeleteEndpoint)

		admin.Post("/user", s.handleAddTenantUser)
		admin.Put("/user/{id}", s.handleAddTenantUser)
		admin.Delete("/user/{id}", s.handleDeleteTenantUser)
	}

	fax := app.Party("/fax", s.basicTenantUserAuthMiddleware)
	{
		fax.Post("/send", s.handleDocumentUpload)
		// New endpoint to retrieve fax logs by job UUID.
		fax.Get("/status", s.getFaxLogByUUID)
	}
}

// -------------------------
// Tenant User Web Handlers
// -------------------------

// handleAuthenticateTenantUser authenticates a tenant user using Basic Auth.
// The credentials are expected to be in the Authorization header in the format "Basic username:password".
// On successful authentication, the handler returns a JSON with a success message, API key, user ID, and tenant ID.
func (s *Server) handleAuthenticateTenantUser(ctx iris.Context) {
	const prefix = "Basic "
	authHeader := ctx.GetHeader("Authorization")
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		unauthorized(ctx, s, "Missing or invalid Authorization header")
		return
	}
	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		unauthorized(ctx, s, "Failed to decode credentials")
		return
	}
	credentials := string(decodedBytes)
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		unauthorized(ctx, s, "Invalid credentials format")
		return
	}
	username := credentials[:colonIndex]
	password := credentials[colonIndex+1:]
	user, err := s.AuthenticateTenantUser(username, password)
	if err != nil {
		unauthorized(ctx, s, "Authentication failed: "+err.Error())
		return
	}
	// Authentication successful.
	ctx.JSON(iris.Map{
		"message":   "authentication successful",
		"api_key":   user.APIKey,
		"user_id":   user.ID,
		"tenant_id": user.TenantID,
	})
}

// handleAddTenantUser reads a JSON payload for a new tenant user, creates the user (encrypting the password),
// and returns the created user.
func (s *Server) handleAddTenantUser(ctx iris.Context) {
	var user TenantUser
	if err := ctx.ReadJSON(&user); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid input: " + err.Error()})
		return
	}
	if err := s.AddTenantUser(&user); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to add tenant user: " + err.Error()})
		return
	}
	ctx.JSON(user)
}

// handleUpdateTenantUser updates an existing tenant user using the ID provided in the URL.
func (s *Server) handleUpdateTenantUser(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid user id"})
		return
	}
	var user TenantUser
	if err := ctx.ReadJSON(&user); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid input: " + err.Error()})
		return
	}
	user.ID = uint(id)
	if err := s.UpdateTenantUser(&user); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update tenant user: " + err.Error()})
		return
	}
	ctx.JSON(user)
}

// handleDeleteTenantUser deletes a tenant user by its ID.
func (s *Server) handleDeleteTenantUser(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid user id"})
		return
	}
	if err := s.DeleteTenantUser(uint(id)); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete tenant user: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"message": "tenant user deleted successfully"})
}

// getTenantUsers retrieves all tenant users. Optionally, you might filter these by tenant ID.
func (s *Server) getTenantUsers(ctx iris.Context) {
	var users []TenantUser
	if err := s.DB.Find(&users).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve tenant users: " + err.Error()})
		return
	}
	ctx.JSON(users)
}

// -------------------------
// Middleware for Tenant User Authentication
// -------------------------

// basicTenantUserAuthMiddleware authenticates tenant users using Basic Auth.
// If authentication fails, it logs the attempt and responds with 401.
/*func (s *Server) basicTenantUserAuthMiddleware(ctx iris.Context) {
	const prefix = "Basic "
	authHeader := ctx.GetHeader("Authorization")
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		unauthorized(ctx, s, "Missing or invalid Authorization header")
		return
	}
	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		unauthorized(ctx, s, "Failed to decode credentials")
		return
	}
	credentials := string(decodedBytes)
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		unauthorized(ctx, s, "Invalid credentials format")
		return
	}
	username := credentials[:colonIndex]
	password := credentials[colonIndex+1:]
	user, err := s.AuthenticateTenantUser(username, password)
	if err != nil {
		unauthorized(ctx, s, "Authentication failed: "+err.Error())
		return
	}
	// Store the authenticated tenant user in context for downstream handlers.
	ctx.Values().Set("tenantUser", user)
	ctx.Next()
}*/

// -------------------------
// Admin Handlers for Tenant Management
// -------------------------

// handleAddTenant creates a new tenant.
func (s *Server) handleAddTenant(ctx iris.Context) {
	var tenant Tenant
	if err := ctx.ReadJSON(&tenant); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if err := s.DB.Create(&tenant).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to add tenant: " + err.Error()})
		return
	}
	// Update in-memory map.
	s.mu.Lock()
	if s.Tenants == nil {
		s.Tenants = make(map[uint]*Tenant)
	}
	s.Tenants[tenant.ID] = &tenant
	s.mu.Unlock()
	ctx.JSON(tenant)
}

// handleUpdateTenant updates an existing tenant using tenant ID from the URL.
func (s *Server) handleUpdateTenant(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid tenant id"})
		return
	}
	var tenant Tenant
	if err := ctx.ReadJSON(&tenant); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	tenant.ID = uint(id)
	if err := s.DB.Save(&tenant).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update tenant: " + err.Error()})
		return
	}
	s.mu.Lock()
	s.Tenants[tenant.ID] = &tenant
	s.mu.Unlock()
	ctx.JSON(tenant)
}

// handleDeleteTenant deletes a tenant by its ID.
func (s *Server) handleDeleteTenant(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid tenant id"})
		return
	}
	if err := s.DB.Delete(&Tenant{}, id).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete tenant: " + err.Error()})
		return
	}
	s.mu.Lock()
	delete(s.Tenants, uint(id))
	s.mu.Unlock()
	ctx.JSON(iris.Map{"message": "tenant deleted successfully"})
}

// -------------------------
// Admin Handlers for Tenant Number Management
// -------------------------

// handleAddTenantNumber reads a JSON payload and calls addTenantNumber to persist the new tenant number.
func (s *Server) handleAddTenantNumber(ctx iris.Context) {
	var tn TenantNumber
	if err := ctx.ReadJSON(&tn); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if err := s.addTenantNumber(tn.TenantID, &tn); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to add tenant number: " + err.Error()})
		return
	}
	ctx.JSON(tn)
}

// handleUpdateTenantNumber updates an existing tenant number.
func (s *Server) handleUpdateTenantNumber(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid tenant number id"})
		return
	}
	var tn TenantNumber
	if err := ctx.ReadJSON(&tn); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	tn.ID = uint(id)
	if err := s.DB.Save(&tn).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update tenant number: " + err.Error()})
		return
	}
	// Update in-memory map.
	s.mu.Lock()
	s.TenantNumbers[tn.Number] = &tn
	s.mu.Unlock()
	ctx.JSON(tn)
}

// handleDeleteTenantNumber deletes a tenant number.
// Expects query parameters: ?number=...&tenant_id=...
func (s *Server) handleDeleteTenantNumber(ctx iris.Context) {
	numberStr := ctx.URLParam("number")
	tenantIDStr := ctx.URLParam("tenant_id")
	if numberStr == "" || tenantIDStr == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "both number and tenant_id are required"})
		return
	}
	tenantID, err := strconv.Atoi(tenantIDStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid tenant_id"})
		return
	}
	if err := s.removeTenantNumber(uint(tenantID), numberStr); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete tenant number: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"message": "tenant number deleted successfully"})
}

// -------------------------
// Admin Handlers for Endpoint Management
// -------------------------

// handleAddEndpoint reads a JSON payload and persists a new endpoint.
func (s *Server) handleAddEndpoint(ctx iris.Context) {
	var ep Endpoint
	if err := ctx.ReadJSON(&ep); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if err := s.addEndpointToDB(&ep); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to add endpoint: " + err.Error()})
		return
	}
	ctx.JSON(ep)
}

// handleUpdateEndpoint updates an existing endpoint using the ID from the URL.
func (s *Server) handleUpdateEndpoint(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid endpoint id"})
		return
	}
	var ep Endpoint
	if err := ctx.ReadJSON(&ep); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	ep.ID = uint(id)
	if err := s.updateEndpoint(&ep); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update endpoint: " + err.Error()})
		return
	}
	ctx.JSON(ep)
}

// handleDeleteEndpoint deletes an endpoint based on the ID provided in the URL.
func (s *Server) handleDeleteEndpoint(ctx iris.Context) {
	idStr := ctx.Params().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid endpoint id"})
		return
	}
	if err := s.removeEndpointFromDB(uint(id)); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete endpoint: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"message": "endpoint deleted successfully"})
}

// handleDocumentUpload receives an uploaded file, validates its type, converts it to TIFF using ImageMagick, and saves it.
// handleDocumentUpload receives an uploaded document, validates it, converts it to TIFF via ImageMagick,
// and enqueues a FaxJob for processing.
// handleDocumentUpload receives an uploaded document, validates it, converts it to TIFF using ImageMagick's 'convert' command,
// and enqueues a FaxJob for processing. It also extracts the source/destination numbers from the request.
func (s *Server) handleDocumentUpload(ctx iris.Context) {
	// Retrieve the file from the form (field name "document")
	file, fileHeader, err := ctx.FormFile("file")
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "failed to get document: " + err.Error()})
		return
	}
	defer file.Close()

	// Validate file extension.
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	allowedExts := map[string]bool{
		".pdf":  true,
		".tif":  true,
		".tiff": true,
	}
	if !allowedExts[ext] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "unsupported file type"})
		return
	}

	// Validate MIME type.
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to read file header: " + err.Error()})
		return
	}
	contentType := http.DetectContentType(buffer[:n])
	allowedContentTypes := map[string]bool{
		"application/pdf": true,
		"image/tiff":      true,
		"image/x-tiff":    true,
	}
	if !allowedContentTypes[contentType] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(iris.Map{"error": fmt.Sprintf("unsupported MIME type: %s", contentType)})
		return
	}
	// Reset file pointer after reading header.
	if _, err := file.Seek(0, 0); err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to reset file pointer: " + err.Error()})
		return
	}

	docID := uuid.New()

	fileFormat := "webhook_fax_%s%s"

	// Save uploaded file to a temporary location.
	tempFile := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(fileFormat, docID, ext))
	tmpFile, err := os.Create(tempFile)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to create temp file: " + err.Error()})
		return
	}
	defer func() {
		tmpFile.Close()
		// Optionally remove the temp file later.
	}()

	if _, err = io.Copy(tmpFile, file); err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to save document: " + err.Error()})
		return
	}

	// Determine output path for TIFF.
	destFile := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(tempFileFormat, docID))
	// Convert the file to TIFF using ImageMagick's 'convert' command.
	//cmdStr := fmt.Sprintf("magick %s -density 204x196 -resize '1728x2156!' -background white -alpha background -compress Group4 %s", tempFile, destFile)
	cmdStr := fmt.Sprintf(
		"gs -q -r204x196 -g1728x2156 -dNOPAUSE -dBATCH -dSAFER -dPDFFitPage -sDEVICE=tiffg3 -sOutputFile=%s -- %s",
		destFile, tempFile,
	)
	cmd := exec.Command("/bin/bash", "-c", cmdStr)
	if err = cmd.Run(); err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to convert document: " + err.Error()})
		return
	}
	os.Remove(tempFile)

	// Extract source/destination numbers and caller name from form fields.
	// Use FormValue to get additional parameters from the request.
	calleeNumber := ctx.FormValue("callee_number")
	callerNumber := ctx.FormValue("caller_number")
	//callerIdName := ctx.FormValue("caller_id_name")

	// todo validate sender for tenants

	// (Optionally, validate these values and return an error if missing.)

	srcNum, err := s.getNumber(callerNumber)
	if err != nil || srcNum.TenantID != ctx.Values().Get("tenantID") {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to validate caller number: " + err.Error()})
		return
	}

	// Create a FaxJob using the converted TIFF and the request parameters.
	faxjob := &FaxJob{
		UUID:           docID,
		CalleeNumber:   calleeNumber, // from request
		CallerIdNumber: callerNumber, // from request
		// CallerIdName:   callerIdName,   // from request
		FileName:   destFile,
		UseECM:     false,
		DisableV17: false,
		Result: &gofaxlib.FaxResult{
			UUID:        uuid.New(),
			StartTs:     time.Now(),
			EndTs:       time.Now().Add(2 * time.Second),
			HangupCause: "WEBHOOK",
			//RemoteID:         "remoteID-placeholder",
			ResultText: "OK",
			Success:    true,
		},
		SourceRoutingInformation: FaxSourceInfo{
			Timestamp:  time.Now(),
			SourceType: "webhook",
			Source:     "placeholder1",
			SourceID:   "placeholder2", // Placeholder – optionally extract from request.
		},
		Ts: time.Now(),
	}

	s.FaxJobRouting <- faxjob

	// Respond to the HTTP request.
	ctx.JSON(iris.Map{
		"message":  "fax enqueued",
		"job_uuid": faxjob.UUID.String(),
	})
}

// basicTenantUserAuthMiddleware authenticates tenant users using Basic Auth.
// It expects the credentials to be in the format "username:password" in the Authorization header.
func (s *Server) basicTenantUserAuthMiddleware(ctx iris.Context) {
	const prefix = "Basic "
	authHeader := ctx.GetHeader("Authorization")
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		unauthorized(ctx, s, "Missing or invalid Authorization header")
		return
	}

	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		unauthorized(ctx, s, "Failed to decode credentials")
		return
	}

	credentials := string(decodedBytes)
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		unauthorized(ctx, s, "Invalid credentials format")
		return
	}

	username := credentials[:colonIndex]
	password := credentials[colonIndex+1:]

	user, err := s.AuthenticateTenantUser(username, password)
	if err != nil {
		unauthorized(ctx, s, "Authentication failed: "+err.Error())
		return
	}

	// Store the authenticated tenant user in context.
	ctx.Values().Set("tenantID", user.TenantID)
	ctx.Next()
}

// basicAuthMiddleware is a middleware that enforces Basic Authentication using an API key
func (s *Server) basicAuthMiddleware(ctx iris.Context) {
	// Retrieve the expected API key from environment variables
	var lm = s.LogManager

	expectedAPIKey := gofaxlib.Config.Web.APIKey
	if expectedAPIKey == "" {
		lm.SendLog(lm.BuildLog(
			"Web.Auth",
			"Authenticated web client",
			logrus.WarnLevel,
			map[string]interface{}{
				"ip": ctx.Values().GetString("client_ip"),
			},
		))
		// Log the error
		/*logf := LoggingFormat{
			Type:    "middleware_auth",
			Level:   logrus.ErrorLevel,
			Message: "API_KEY environment variable not set",
		}
		logf.Print()*/

		// Respond with 500 Internal Server Error
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.WriteString("Internal Server Error")
		return
	}

	// Get the Authorization header
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		// Missing Authorization header
		unauthorized(ctx, s, "Authorization header missing")
		return
	}

	// Check if the Authorization header starts with "Basic "
	const prefix = "Basic "
	if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
		// Invalid Authorization header format
		unauthorized(ctx, s, "Invalid Authorization header format")
		return
	}

	// Decode the Base64 encoded credentials
	encodedCredentials := authHeader[len(prefix):]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		// Failed to decode credentials
		unauthorized(ctx, s, "Failed to decode credentials")
		return
	}
	credentials := string(decodedBytes)

	// In Basic Auth, credentials are in the format "username:password"
	colonIndex := indexOf(credentials, ':')
	if colonIndex < 0 {
		// Invalid credentials format
		unauthorized(ctx, s, "Invalid credentials format")
		return
	}

	// Extract the API key (password) from the credentials
	// Username can be ignored or used as needed
	// For this example, we'll assume the API key is the password
	apiKey := credentials[colonIndex+1:]

	// Compare the provided API key with the expected one
	if apiKey != expectedAPIKey {
		// Invalid API key
		unauthorized(ctx, s, "Invalid API key")
		return
	}

	// Authentication successful, proceed to the handler
	ctx.Next()
}

// indexOf finds the index of the first occurrence of sep in s
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}

// unauthorized responds with a 401 status and a WWW-Authenticate header
func unauthorized(ctx iris.Context, gateway *Server, message string) {
	// Log the unauthorized access attempt
	var lm = gateway.LogManager
	lm.SendLog(lm.BuildLog(
		"Web.AuthMiddleware",
		"Unauthorized web client",
		logrus.ErrorLevel,
		map[string]interface{}{
			"ip": ctx.Values().GetString("client_ip"),
		},
	))

	// Set the WWW-Authenticate header to indicate Basic Auth is required
	ctx.Header("WWW-Authenticate", `Basic realm="Restricted"`)

	// Respond with 401 Unauthorized
	ctx.StatusCode(http.StatusUnauthorized)
	ctx.WriteString(message)
}

// getFaxLogByUUID retrieves all fax job result records that match the provided UUID query parameter.
func (s *Server) getFaxLogByUUID(ctx iris.Context) {
	uuidStr := ctx.URLParam("uuid")
	if uuidStr == "" {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "uuid query parameter is required"})
		return
	}

	// Parse the provided UUID string.
	jobUUID, err := uuid.Parse(uuidStr)
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid uuid format: " + err.Error()})
		return
	}

	var records []FaxJobResultRecord
	if err := s.DB.Where("job_uuid = ?", jobUUID).Find(&records).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve fax logs: " + err.Error()})
		return
	}

	ctx.JSON(records)
}
