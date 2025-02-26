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
	"strings"
	"time"
)

// loadWebPaths sets up HTTP routes.
func (s *Server) loadWebPaths(app *iris.Application) {
	party := app.Party("/send", s.basicAuthMiddleware)
	{
		party.Get("/", func(ctx iris.Context) {
			ctx.JSON(iris.Map{"message": "send endpoint"})
		})
		party.Post("/document", s.handleDocumentUpload)
	}
}

// handleDocumentUpload receives an uploaded file, validates its type, converts it to TIFF using ImageMagick, and saves it.
// handleDocumentUpload receives an uploaded document, validates it, converts it to TIFF via ImageMagick,
// and enqueues a FaxJob for processing.
// handleDocumentUpload receives an uploaded document, validates it, converts it to TIFF using ImageMagick's 'convert' command,
// and enqueues a FaxJob for processing. It also extracts the source/destination numbers from the request.
func (s *Server) handleDocumentUpload(ctx iris.Context) {
	// Retrieve the file from the form (field name "document")
	file, fileHeader, err := ctx.FormFile("document")
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
	cmdStr := fmt.Sprintf("convert %s -density 204x196 -units pixelsperinch -resize '1728x2156!' -quality 100 -background white -alpha background -alpha off -compress Fax %s", tempFile, destFile)
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
	callerIdNumber := ctx.FormValue("caller_id_number")
	callerIdName := ctx.FormValue("caller_id_name")

	// todo validate sender for tenants

	// (Optionally, validate these values and return an error if missing.)

	// Create a FaxJob using the converted TIFF and the request parameters.
	faxjob := &FaxJob{
		UUID:           docID,
		CalleeNumber:   calleeNumber,   // from request
		CallerIdNumber: callerIdNumber, // from request
		CallerIdName:   callerIdName,   // from request
		FileName:       destFile,
		UseECM:         false,
		DisableV17:     false,
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

	// Enqueue the fax job for processing.
	s.Queue.QueueFaxResult <- QueueFaxResult{
		Job:      faxjob,
		Success:  faxjob.Result.Success,
		Response: faxjob.Result.ResultText,
	}

	s.FaxJobRouting <- faxjob

	// Respond to the HTTP request.
	ctx.JSON(iris.Map{
		"message":  "fax enqueued",
		"job_uuid": faxjob.UUID.String(),
	})
}

// basicAuthMiddleware is a middleware that enforces Basic Authentication using an API key
func (s *Server) basicAuthMiddleware(ctx iris.Context) {
	// Retrieve the expected API key from environment variables
	var lm = s.LogManager

	expectedAPIKey := os.Getenv("API_KEY")
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
