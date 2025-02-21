package gofaxlib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (lm *LogManager) LoadTemplates() {
	templates := map[string]string{
		"GenericError":       "An error occurred: %v",
		"UnexpectedError":    "Unexpected error: %v",
		"UnhandledException": "Unhandled exception: %v",
	}

	for name, template := range templates {
		lm.AddTemplate(name, template)
	}
}

// LogManager manages log templates and handles dispatching logs to Loki.
type LogManager struct {
	Templates  map[string]string
	LokiClient *LokiClient
	LogChannel chan *LoggingFormat
	wg         sync.WaitGroup
}

// LoggingFormat represents the structure of a log message.
type LoggingFormat struct {
	Message        string                 `json:"message,omitempty"`
	Error          error                  `json:"error,omitempty"`
	Type           string                 `json:"type,omitempty"`
	Level          logrus.Level           `json:"level,omitempty"`
	AdditionalData map[string]interface{} `json:"additional_data,omitempty"`
	Timestamp      time.Time              `json:"timestamp,omitempty"`
}

// LogEntry represents a log entry for Loki.
type LogEntry struct {
	Timestamp time.Time
	Line      string
}

// LokiPushData represents the data structure required by Loki's push API.
type LokiPushData struct {
	Streams []LokiStream `json:"streams"`
}

// LokiStream represents a stream of logs with the same labels in Loki.
type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // Array of [timestamp, line] tuples
}

// LokiClient handles interactions with the Loki service.
type LokiClient struct {
	PushURL  string
	Username string
	Password string
	Job      string
}

// NewLokiClient initializes a new Loki client.
func NewLokiClient() *LokiClient {
	return &LokiClient{
		PushURL:  Config.Loki.PushURL,
		Username: Config.Loki.Username,
		Password: Config.Loki.Password,
		Job:      Config.Loki.Job,
	}
}

// PushLog sends a log entry to Loki.
func (c *LokiClient) PushLog(labels map[string]string, entry LogEntry) error {
	payload := LokiPushData{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][2]string{
					{strconv.FormatInt(entry.Timestamp.UnixNano(), 10), entry.Line},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON payload: %w", err)
	}

	req, err := http.NewRequest("POST", c.PushURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to Loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response from Loki: %d", resp.StatusCode)
	}

	return nil
}

// NewLogManager initializes a new LogManager.
func NewLogManager(lokiClient *LokiClient) *LogManager {
	lm := &LogManager{
		Templates:  make(map[string]string),
		LokiClient: lokiClient,
		LogChannel: make(chan *LoggingFormat),
	}
	lm.wg.Add(1)
	go lm.processLogChannel()
	return lm
}

// AddTemplate adds a new log template to the manager.
func (lm *LogManager) AddTemplate(name, template string) {
	lm.Templates[strings.ToUpper(name)] = template
}

// BuildLog creates and formats a log message dynamically.
func (lm *LogManager) BuildLog(logType string, templateName string, level logrus.Level, fields map[string]interface{}, args ...interface{}) *LoggingFormat {
	message := lm.formatTemplate(templateName, args...)
	return &LoggingFormat{
		Message:        message,
		Type:           strings.ToUpper(logType),
		Level:          level,
		AdditionalData: fields,
		Timestamp:      time.Now(),
	}
}

// AddField adds a new field to an already built log.
func (lf *LoggingFormat) AddField(key string, value interface{}) {
	if lf.AdditionalData == nil {
		lf.AdditionalData = make(map[string]interface{})
	}
	lf.AdditionalData[key] = value
}

// formatTemplate formats a template with provided arguments.
func (lm *LogManager) formatTemplate(templateName string, args ...interface{}) string {
	template, exists := lm.Templates[strings.ToUpper(templateName)]
	if !exists {
		return fmt.Sprintf(templateName, args...)
	}
	return fmt.Sprintf(template, args...)
}

// SendLog sends a log to Loki asynchronously via the log channel.
func (lm *LogManager) SendLog(log *LoggingFormat) {
	log.Print()
	lm.LogChannel <- log
}

// processLogChannel processes logs from the channel and sends them to Loki.
func (lm *LogManager) processLogChannel() {
	defer lm.wg.Done()
	for log := range lm.LogChannel {
		labels := map[string]string{
			"job":  lm.LokiClient.Job,
			"type": log.Type,
		}
		logLine := log.String()
		entry := LogEntry{
			Timestamp: log.Timestamp,
			Line:      logLine,
		}
		if err := lm.LokiClient.PushLog(labels, entry); err != nil {
			logrus.Error("Failed to send log to Loki:", err)
		}
	}
}

// Print outputs the log locally (stdout or logrus).
func (lf *LoggingFormat) Print() {
	logEntry := logrus.WithFields(logrus.Fields{
		"type":  lf.Type,
		"level": lf.Level.String(),
		"time":  lf.Timestamp.Format(time.RFC3339),
	})

	for key, value := range lf.AdditionalData {
		logEntry = logEntry.WithField(key, value)
	}

	switch lf.Level {
	case logrus.ErrorLevel:
		logEntry.Error(lf.Message)
	case logrus.WarnLevel:
		logEntry.Warn(lf.Message)
	case logrus.DebugLevel:
		logEntry.Debug(lf.Message)
	default:
		logEntry.Info(lf.Message)
	}
}

// String serializes the LoggingFormat into JSON for Loki.
func (lf *LoggingFormat) String() string {
	data, err := json.Marshal(lf)
	if err != nil {
		return fmt.Sprintf("Error serializing log: %v", err)
	}
	return string(data)
}

// CloseLogManager gracefully shuts down the log manager and waits for the log channel to empty.
func (lm *LogManager) CloseLogManager() {
	close(lm.LogChannel)
	lm.wg.Wait()
}
