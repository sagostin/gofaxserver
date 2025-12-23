package gofaxserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gofaxserver/gofaxlib"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sirupsen/logrus"
)

// QueueFaxResult holds the result of a fax send attempt for a specific endpoint group.
type QueueFaxResult struct {
	Job *FaxJob `json:"job"`
	Err error   `json:"error,omitempty"`
}

// Queue represents a fax job processing queue.
type Queue struct {
	Queue          chan *FaxJob
	server         *Server
	QueueFaxResult chan QueueFaxResult
}

// NewQueue creates a new Queue.
func NewQueue(s *Server) *Queue {
	return &Queue{
		Queue:          make(chan *FaxJob),
		QueueFaxResult: make(chan QueueFaxResult),
		server:         s,
	}
}

// Start pulls fax jobs from the Queue and processes each asynchronously.
func (q *Queue) Start() {
	go q.startQueueResults()
	for {
		fax := <-q.Queue
		go q.processFax(fax)
	}
}

type FaxJobWithFile struct {
	FaxJob
	FileData string `json:"file_data"`
}

// helper: detect "global upstream gateway" group (keep list intact)
func isGlobalUpstreamGatewayGroup(group []*Endpoint) bool {
	if len(group) == 0 {
		return false
	}
	for _, ep := range group {
		if !(strings.EqualFold(ep.Type, "global") &&
			ep.TypeID == 0 &&
			strings.EqualFold(ep.EndpointType, "gateway")) {
			return false
		}
	}
	return true
}

func (q *Queue) processFax(f *FaxJob) {
	// Ensure fax is tracked in the FaxTracker (may already be tracked from inbound)
	q.server.FaxTracker.Begin(f)

	// Ensure the tracker is marked complete exactly once, on any exit path.
	var completeOnce sync.Once
	complete := func(reason string) {
		completeOnce.Do(func() {
			q.server.FaxTracker.Complete(f.UUID)
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue", "fax job marked complete",
				logrus.InfoLevel,
				map[string]interface{}{"uuid": f.UUID.String(), "reason": reason},
			))
		})
	}
	// Safety net in case we return anywhere without an explicit reason.
	defer complete("function-exit")

	// ----------------- Build endpoint groups: type -> priority -> endpoints
	groupMap := make(map[string]map[uint][]*Endpoint, 4)
	for _, ep := range f.Endpoints {
		if groupMap[ep.EndpointType] == nil {
			groupMap[ep.EndpointType] = make(map[uint][]*Endpoint)
		}
		groupMap[ep.EndpointType][ep.Priority] = append(groupMap[ep.EndpointType][ep.Priority], ep)
	}

	// ----------------- Thread-safe results snapshots for later notifications
	notifyFaxResults := NotifyFaxResults{
		Results: make(map[string]*FaxJob),
		FaxJob:  f,
	}
	var resultsMu sync.Mutex
	sendResult := func(ff *FaxJob) {
		// async fan-out (non-blocking channel assumed)
		q.QueueFaxResult <- QueueFaxResult{Job: ff}

		// snapshot with lock
		copyFax := *ff
		resultsMu.Lock()
		notifyFaxResults.Results[ff.CallUUID.String()] = &copyFax
		resultsMu.Unlock()
	}

	// ----------------- Retry config (safe defaults)
	maxAttempts := 3
	if s := gofaxlib.Config.Faxing.RetryAttempts; s != "" {
		if rMax, err := strconv.Atoi(s); err == nil && rMax > 0 {
			maxAttempts = rMax
		}
	}
	baseDelay := time.Minute
	if d, err := ParseDuration(gofaxlib.Config.Faxing.RetryDelay); err == nil && d > 0 {
		baseDelay = d
	}
	maxDelay := 60 * time.Second

	// ----------------- Small utilities (logging & stamping)
	logAttempt := func(level logrus.Level, msg string, fields map[string]interface{}) {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog("Queue", msg, level, fields))
	}

	stampAndSend := func(ff *FaxJob, start time.Time, success bool, humanStatus string) {
		if ff.Result == nil {
			ff.Result = &gofaxlib.FaxResult{}
		}
		ff.Result.UUID = ff.CallUUID
		ff.Result.Success = success
		ff.Result.StartTs = start
		ff.Result.EndTs = time.Now()
		if !success {
			if ff.Result.HangupCause == "" && humanStatus != "" {
				ff.Status = humanStatus
			}
		} else if humanStatus != "" {
			ff.Status = humanStatus
		}
		sendResult(ff)
	}

	// retryWithBackoff runs fn up to maxAttempts with exponential backoff + jitter.
	retryWithBackoff := func(ctx context.Context, maxAttempts int, base, max time.Duration, fn func(attempt int) (ok bool, retriable bool)) (ok bool) {
		delay := base
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return false
			}
			ok, retriable := fn(attempt)
			if ok {
				return true
			}
			if !retriable || attempt == maxAttempts {
				return false
			}
			// ±10% jitter
			jit := time.Duration(rand.Int63n(int64(delay)/5)) - delay/10
			sleep := delay + jit
			if sleep > max {
				sleep = max
			}
			// Show "waiting" in tracker and sleep, respecting ctx
			q.server.FaxTracker.MarkWaiting(f.UUID)
			select {
			case <-time.After(sleep):
			case <-ctx.Done():
				return false
			}
			delay *= 2
			if delay > max {
				delay = max
			}
		}
		return false
	}

	// helpers to avoid nil derefs in logs
	safeResultText := func(ff *FaxJob) string {
		if ff != nil && ff.Result != nil {
			return ff.Result.ResultText
		}
		return ""
	}
	safeHangup := func(ff *FaxJob) string {
		if ff != nil && ff.Result != nil {
			return ff.Result.HangupCause
		}
		return ""
	}
	endpointBriefOr := func(ep *Endpoint, fallback string) (epType, epLabel, epVal string) {
		if ep == nil {
			return "unknown", "unknown", fallback
		}
		return endpointBrief(ep)
	}

	// ----------------- Run workers per endpoint type
	var wg sync.WaitGroup
	for endpointType, prioMap := range groupMap {
		epType := endpointType
		typeMap := prioMap

		wg.Add(1)
		go func() {
			defer wg.Done()

			// sort priorities (999 always last)
			prios := make([]uint, 0, len(typeMap))
			for p := range typeMap {
				prios = append(prios, p)
			}
			sort.Slice(prios, func(i, j int) bool {
				a, b := prios[i], prios[j]
				switch {
				case a == 999 && b != 999:
					return false
				case b == 999 && a != 999:
					return true
				default:
					return a < b
				}
			})

		prioLoop: // ← label so we can break out of ALL priorities on success
			for _, prio := range prios {
				group := typeMap[prio]
				if len(group) == 0 {
					continue
				}

				switch epType {
				case "gateway":
					// banner
					{
						gws := make([]string, 0, len(group))
						for _, e := range group {
							gws = append(gws, gatewayLabel(e))
						}
						logAttempt(logrus.InfoLevel, "processing gateway group", map[string]interface{}{
							"uuid":           f.UUID.String(),
							"priority":       prio,
							"group_size":     len(group),
							"gateways":       gws,
							"endpoint_type":  "gateway",
							"max_attempts":   maxAttempts,
							"base_delay_sec": int(baseDelay.Seconds()),
						})
					}

					// Normalize numbers once
					srcNum := q.server.DialplanManager.ApplyTransformationRules(f.CallerIdNumber)
					dstNum := q.server.DialplanManager.ApplyTransformationRules(f.CalleeNumber)

					// Bridge routing check
					_, enableBridge := q.server.Router.detectAndRouteToBridge(dstNum, srcNum, f.SourceInfo.Source)

					// --- Strategy A: FS global fan-out (single aggregated “endpoint”)
					if isGlobalUpstreamGatewayGroup(group) {
						ff := *f
						ff.Result = &gofaxlib.FaxResult{}

						logAttempt(logrus.InfoLevel, "gateway strategy: global-batch (FS fan-out/failover)",
							map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "group_size": len(ff.Endpoints)})

						agg := &Endpoint{
							Type:         "global",
							EndpointType: "gateway",
							Endpoint:     strings.Join(q.server.UpstreamFsGateways, ","),
						}
						epTy, epLbl, epVal := endpointBrief(agg)

						// ✅ One-shot ONLY when bridge is ENABLED
						oneShot := enableBridge

						sendOnce := func(attempt int) (bool, bool) {
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()
							q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
							q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epTy, epLbl, epVal)

							start := time.Now()
							returned, err := q.server.FsSocket.SendFax(&ff)
							elapsed := time.Since(start)

							if err != nil {
								retri := (returned == SendRetry)
								level := map[bool]logrus.Level{true: logrus.WarnLevel, false: logrus.ErrorLevel}[retri]
								logAttempt(level, "fax send error (gateway global-batch)", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"priority":   prio,
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
									"error":      err.Error(),
									"returned":   fmt.Sprintf("%v", returned),
									"retriable":  retri,
								})
								if !retri && ff.Result != nil && ff.Result.HangupCause == "" {
									ff.Result.HangupCause = ff.Status
								}
								stampAndSend(&ff, start, false, "send error")
								return false, retri
							}

							if ff.Result != nil && ff.Result.Success {
								logAttempt(logrus.InfoLevel, "fax sent successfully (gateway global-batch)", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"priority":   prio,
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
								})
								stampAndSend(&ff, start, true, "")
								return true, false
							}

							logAttempt(logrus.WarnLevel, "fax failed (gateway global-batch explicit failure)", map[string]interface{}{
								"uuid":         f.UUID.String(),
								"call_uuid":    ff.CallUUID.String(),
								"priority":     prio,
								"attempt":      attempt,
								"elapsed_ms":   int(elapsed.Milliseconds()),
								"result_text":  safeResultText(&ff),
								"hangup_cause": safeHangup(&ff),
							})
							stampAndSend(&ff, start, false, "gateway failure")
							return false, true
						}

						var ok bool
						if oneShot {
							ok, _ = sendOnce(-1) // single shot when bridge is enabled
						} else {
							ok = retryWithBackoff(context.Background(), maxAttempts, baseDelay, maxDelay, func(attempt int) (bool, bool) {
								return sendOnce(attempt)
							})
						}

						if !ok {
							logAttempt(logrus.ErrorLevel, "gateway global-batch group exhausted without success",
								map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "group_size": len(group)})
							// escalate to next priority
							continue
						}
						// success → stop escalating
						break
					}

					// --- Strategy B: Per-endpoint sequential with retries
					logAttempt(logrus.InfoLevel, "gateway strategy: per-endpoint (split & track individually)",
						map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "group_size": len(group)})

					allFailed := true
					for _, ep := range group {
						ff := *f
						ff.Endpoints = []*Endpoint{ep}
						ff.Result = &gofaxlib.FaxResult{}

						epTy, epLbl, epVal := endpointBrief(ep)
						logAttempt(logrus.InfoLevel, "processing single gateway endpoint", map[string]interface{}{
							"uuid":     f.UUID.String(),
							"priority": prio,
							"endpoint": endpointSummary(ep),
						})

						sendOne := func(attempt int) (bool, bool) {
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()
							q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
							q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epTy, epLbl, epVal)

							start := time.Now()
							returned, err := q.server.FsSocket.SendFax(&ff)
							elapsed := time.Since(start)

							if err != nil {
								retri := (returned == SendRetry)
								level := map[bool]logrus.Level{true: logrus.WarnLevel, false: logrus.ErrorLevel}[retri]
								logAttempt(level, "fax send error (gateway single)", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"priority":   prio,
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
									"error":      err.Error(),
									"returned":   fmt.Sprintf("%v", returned),
									"retriable":  retri,
								})
								if !retri && ff.Result != nil && ff.Result.HangupCause == "" {
									ff.Result.HangupCause = ff.Status
								}
								stampAndSend(&ff, start, false, "send error")
								return false, retri
							}

							if ff.Result != nil && ff.Result.Success {
								logAttempt(logrus.InfoLevel, "fax sent successfully (gateway single)", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"priority":   prio,
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
								})
								stampAndSend(&ff, start, true, "")
								return true, false
							}

							logAttempt(logrus.WarnLevel, "fax failed (gateway single explicit failure)", map[string]interface{}{
								"uuid":         f.UUID.String(),
								"call_uuid":    ff.CallUUID.String(),
								"priority":     prio,
								"attempt":      attempt,
								"elapsed_ms":   int(elapsed.Milliseconds()),
								"result_text":  safeResultText(&ff),
								"hangup_cause": safeHangup(&ff),
							})
							stampAndSend(&ff, start, false, "gateway failure")
							return false, true
						}

						ok := retryWithBackoff(context.Background(), maxAttempts, baseDelay, maxDelay, func(attempt int) (bool, bool) {
							return sendOne(attempt)
						})
						if ok {
							allFailed = false
							// Continue processing other endpoints at same priority if you want
							// parallel delivery; here we short-circuit on first success.
							break
						}
						logAttempt(logrus.ErrorLevel, "endpoint exhausted without success", map[string]interface{}{
							"uuid":     f.UUID.String(),
							"priority": prio,
							"endpoint": endpointSummary(ep),
						})
					}

					if allFailed {
						// escalate to next priority
						continue
					}
					// success → stop escalating further priorities
					break

				case "webhook":
					// Prepare payload once per priority group
					var (
						webhookPDFPath string
						webhookFileB64 string
						fileSHA256     string
						fileSize       int64
					)
					{
						start := time.Now()
						pdf, err := tiffToPdf(f.FileName)
						if err != nil {
							logAttempt(logrus.ErrorLevel, "failed to convert tiff to pdf", map[string]interface{}{
								"uuid": f.UUID.String(), "source_file": f.FileName, "error": err.Error(),
							})
						} else {
							webhookPDFPath = pdf
							if b, rErr := os.ReadFile(pdf); rErr != nil {
								logAttempt(logrus.ErrorLevel, "failed to read fax file for webhook", map[string]interface{}{
									"uuid": f.UUID.String(), "pdf_path": pdf, "error": rErr.Error(),
								})
							} else {
								fileSize = int64(len(b))
								sum := sha256.Sum256(b)
								fileSHA256 = hex.EncodeToString(sum[:])
								webhookFileB64 = base64.StdEncoding.EncodeToString(b)
								logAttempt(logrus.InfoLevel, "webhook payload prepared", map[string]interface{}{
									"uuid":         f.UUID.String(),
									"pdf_path":     webhookPDFPath,
									"file_bytes":   fileSize,
									"file_kb":      fileSize / 1024,
									"sha256":       fileSHA256,
									"elapsed_ms":   int(time.Since(start).Milliseconds()),
									"endpoint_cnt": len(group),
								})
							}
						}
						if webhookPDFPath != "" {
							defer func(path string) { _ = os.Remove(path) }(webhookPDFPath)
						}
					}

					isRetriableStatus := func(code int) bool {
						switch {
						case code == http.StatusRequestTimeout, code == http.StatusTooManyRequests:
							return true
						case code >= 500 && code <= 599:
							return true
						default:
							return false
						}
					}

					allFailed := true
					for _, ep := range group {
						ff := *f
						ff.Endpoints = []*Endpoint{ep}
						ff.Result = &gofaxlib.FaxResult{}

						epTy, epLbl, epVal := endpointBriefOr(ep, "webhook")

						sendWebhookOnce := func(attempt int) (bool, bool) {
							start := time.Now()

							if webhookPDFPath == "" || webhookFileB64 == "" {
								logAttempt(logrus.ErrorLevel, "webhook payload not prepared", map[string]interface{}{
									"uuid": f.UUID.String(), "endpoint": endpointSummary(ep), "attempt": attempt,
								})
								stampAndSend(&ff, start, false, "webhook payload not prepared")
								return false, false
							}

							// Build body
							body, mErr := json.Marshal(FaxJobWithFile{
								FaxJob:   ff,
								FileData: webhookFileB64,
							})
							if mErr != nil {
								logAttempt(logrus.ErrorLevel, "failed to marshal webhook payload", map[string]interface{}{
									"uuid": f.UUID.String(), "endpoint": endpointSummary(ep), "attempt": attempt, "error": mErr.Error(),
								})
								stampAndSend(&ff, start, false, "marshal error")
								return false, false
							}

							// Track attempt (updates tracker each try)
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()
							q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
							q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epTy, epLbl, epVal)

							// Request w/ per-try timeout
							tryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
							defer cancel()

							req, rErr := http.NewRequestWithContext(tryCtx, http.MethodPost, ep.Endpoint, bytes.NewReader(body))
							if rErr != nil {
								logAttempt(logrus.ErrorLevel, "error creating webhook request", map[string]interface{}{
									"uuid": f.UUID.String(), "endpoint": endpointSummary(ep), "attempt": attempt, "error": rErr.Error(),
								})
								stampAndSend(&ff, start, false, "request creation error")
								return false, false
							}
							req.Header.Set("Content-Type", "application/json")
							req.Header.Set("X-Fax-UUID", ff.UUID.String())
							req.Header.Set("X-Call-UUID", ff.CallUUID.String())
							req.Header.Set("X-Attempt", strconv.Itoa(attempt))
							req.Header.Set("X-File-Bytes", strconv.FormatInt(fileSize, 10))
							req.Header.Set("X-File-SHA256", fileSHA256)

							client := &http.Client{
								Timeout: 10 * time.Second,
								Transport: &http.Transport{
									Proxy:               http.ProxyFromEnvironment,
									MaxIdleConns:        100,
									MaxIdleConnsPerHost: 10,
									IdleConnTimeout:     90 * time.Second,
								},
								CheckRedirect: func(req *http.Request, via []*http.Request) error {
									return http.ErrUseLastResponse
								},
							}

							resp, sErr := client.Do(req)
							elapsed := time.Since(start)
							if sErr != nil {
								logAttempt(logrus.ErrorLevel, "webhook send error", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"endpoint":   endpointSummary(ep),
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
									"error":      sErr.Error(),
									"file_kb":    fileSize / 1024,
									"payload_kb": len(body) / 1024,
								})
								stampAndSend(&ff, start, false, "webhook request error")
								return false, true // likely retriable network error
							}
							defer resp.Body.Close()

							if resp.StatusCode >= 200 && resp.StatusCode < 300 {
								logAttempt(logrus.InfoLevel, "webhook delivered", map[string]interface{}{
									"uuid":       f.UUID.String(),
									"call_uuid":  ff.CallUUID.String(),
									"endpoint":   endpointSummary(ep),
									"attempt":    attempt,
									"elapsed_ms": int(elapsed.Milliseconds()),
									"status":     resp.StatusCode,
									"file_kb":    fileSize / 1024,
									"payload_kb": len(body) / 1024,
								})
								stampAndSend(&ff, start, true, fmt.Sprintf("status %d", resp.StatusCode))
								return true, false
							}

							// non-2xx: capture small response
							const maxErrBody = 1024
							limited := io.LimitReader(resp.Body, maxErrBody)
							respSnippet, _ := io.ReadAll(limited)

							retri := isRetriableStatus(resp.StatusCode)
							level := map[bool]logrus.Level{true: logrus.WarnLevel, false: logrus.ErrorLevel}[retri]
							logAttempt(level, "webhook non-2xx response", map[string]interface{}{
								"uuid":       f.UUID.String(),
								"call_uuid":  ff.CallUUID.String(),
								"endpoint":   endpointSummary(ep),
								"attempt":    attempt,
								"elapsed_ms": int(elapsed.Milliseconds()),
								"status":     resp.StatusCode,
								"retriable":  retri,
								"resp_body":  string(respSnippet),
								"file_kb":    fileSize / 1024,
								"payload_kb": len(body) / 1024,
							})
							stampAndSend(&ff, start, false, fmt.Sprintf("status %d", resp.StatusCode))
							return false, retri
						}

						ok := retryWithBackoff(context.Background(), maxAttempts, baseDelay, maxDelay, sendWebhookOnce)
						if ok {
							allFailed = false
							// stop escalating this priority set on first success (within this priority)
							break
						}
						logAttempt(logrus.ErrorLevel, "webhook endpoint exhausted without success", map[string]interface{}{
							"uuid":     f.UUID.String(),
							"endpoint": endpointSummary(ep),
							"attempts": maxAttempts,
						})
					}

					if allFailed {
						// escalate to next priority
						continue
					}
					// ✅ success → stop escalating ALL further priorities for this endpoint type
					break prioLoop

				default:
					logAttempt(logrus.WarnLevel, "unknown endpoint type (skipping priority group)", map[string]interface{}{
						"uuid": f.UUID.String(), "endpoint_type": epType, "priority": prio, "count": len(group),
					})
					// escalate (treat as failed)
					continue
				}
			}
		}()
	}
	wg.Wait()

	// ----------------- Build first-page preview & notify destinations
	defer func(path string) {
		if err := os.Remove(path); err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue", "failed to remove fax file", logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String(), "file": path},
			))
		}
	}(f.FileName)

	fpTiff, err := firstPageTiff(f.UUID.String(), f.FileName)
	if err != nil {
		// mark complete before returning on preview failure
		complete("preview-failed")
		return
	}
	defer func(path string) {
		if err := os.Remove(path); err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue", "failed to remove first page fax file", logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String(), "file": path},
			))
		}
	}(fpTiff)

	notifyDestinations, err := q.processNotifyDestinations(f)
	if err != nil {
		complete("notify-destinations-error")
		fmt.Println("Error processing notify destinations:", err)
		return
	}

	q.processNotifyDestinationsAsync(notifyFaxResults, notifyDestinations, fpTiff)

	// Mark complete AFTER notifications are dispatched
	complete("notify-dispatched")
}

// Summarize a single endpoint for logs.
func endpointSummary(ep *Endpoint) string {
	return fmt.Sprintf("id=%d type=%s type_id=%d ep_type=%s endpoint=%s prio=%d bridge=%t",
		ep.ID, ep.Type, ep.TypeID, ep.EndpointType, ep.Endpoint, ep.Priority, ep.Bridge)
}

// Extract a stable gateway label for log lists (strip :IP if present).
func gatewayLabel(ep *Endpoint) string {
	if ep == nil {
		return ""
	}
	parts := strings.SplitN(ep.Endpoint, ":", 2)
	return parts[0]
}

func endpointBrief(ep *Endpoint) (etype, label, value string) {
	if ep == nil {
		return "", "", ""
	}
	return ep.EndpointType, ep.Type, ep.Endpoint
}
