package gofaxserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"gofaxserver/gofaxlib"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// ---- GROUP ENDPOINTS BY TYPE THEN PRIORITY --------------------------------
	groupMap := make(map[string]map[uint][]*Endpoint, 4)
	for _, ep := range f.Endpoints {
		if groupMap[ep.EndpointType] == nil {
			groupMap[ep.EndpointType] = make(map[uint][]*Endpoint)
		}
		groupMap[ep.EndpointType][ep.Priority] = append(groupMap[ep.EndpointType][ep.Priority], ep)
	}

	// ---- THREAD-SAFE RESULTS SNAPSHOTS ----------------------------------------
	notifyFaxResults := NotifyFaxResults{
		Results: make(map[string]*FaxJob),
		FaxJob:  f,
	}

	var resultsMu sync.Mutex
	sendResult := func(ff *FaxJob) {
		// fan-out to async channel (thread-safe)
		q.QueueFaxResult <- QueueFaxResult{Job: ff}

		// snapshot into shared map (needs a lock)
		copyFax := *ff
		resultsMu.Lock()
		notifyFaxResults.Results[ff.CallUUID.String()] = &copyFax
		resultsMu.Unlock()
	}

	// ---- PARSE GLOBAL RETRY CONFIG ONCE ---------------------------------------
	maxAttempts := 3
	if s := gofaxlib.Config.Faxing.RetryAttempts; s != "" {
		if rMax, err := strconv.Atoi(s); err == nil && rMax > 0 {
			maxAttempts = rMax
		}
	}
	baseDelay := time.Minute
	if d, err := ParseDuration(gofaxlib.Config.Faxing.RetryDelay); err == nil {
		baseDelay = d
	}

	// ---- WORKERS PER ENDPOINT TYPE -------------------------------------------
	var wg sync.WaitGroup
	for endpointType, prioMap := range groupMap {
		wg.Add(1)
		go func(epType string, prioMap map[uint][]*Endpoint) {
			defer wg.Done()

			// collect/sort priorities; keep 999 always last
			prios := make([]uint, 0, len(prioMap))
			for prio := range prioMap {
				prios = append(prios, prio)
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

			// advance-on-any-failure semantics:
			// move to the NEXT priority only if ANY endpoint in the current priority ultimately failed
			for _, prio := range prios {
				group := prioMap[prio]
				groupHadFailure := false

				switch epType {
				case "gateway":
					// banner
					{
						gws := make([]string, 0, len(group))
						for _, e := range group {
							gws = append(gws, gatewayLabel(e))
						}
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue", "processing gateway group", logrus.InfoLevel,
							map[string]interface{}{
								"uuid":           f.UUID.String(),
								"priority":       prio,
								"group_size":     len(group),
								"gateways":       gws,
								"endpoint_type":  "gateway",
								"max_attempts":   maxAttempts,
								"base_delay_sec": int(baseDelay.Seconds()),
							},
						))
					}

					// Strategy A: single global batch (FreeSWITCH handles fan-out/failover)
					if isGlobalUpstreamGatewayGroup(group) {
						ff := *f
						ff.Result = &gofaxlib.FaxResult{}

						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue", "gateway strategy: global-batch (FreeSWITCH handles fan-out/failover)", logrus.InfoLevel,
							map[string]interface{}{
								"uuid":       f.UUID.String(),
								"priority":   prio,
								"group_size": len(ff.Endpoints),
							},
						))

						success := false
						delay := baseDelay

						for attempt := 1; attempt <= maxAttempts; attempt++ {
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()

							epType, epLabel, epVal := endpointBrief(&Endpoint{Type: "global", EndpointType: "gateway",
								Endpoint: strings.Join(q.server.UpstreamFsGateways, ",")}) // for single-endpoint case
							q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
							q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epType, epLabel, epVal)

							startTime := time.Now()
							returned, err := q.server.FsSocket.SendFax(&ff)
							elapsed := time.Since(startTime)

							if err != nil {
								retriable := (returned == SendRetry)
								level := map[bool]logrus.Level{true: logrus.WarnLevel, false: logrus.ErrorLevel}[retriable]
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", "fax send error (gateway global-batch)", level,
									map[string]interface{}{
										"uuid":       f.UUID.String(),
										"call_uuid":  ff.CallUUID.String(),
										"priority":   prio,
										"attempt":    attempt,
										"elapsed_ms": int(elapsed.Milliseconds()),
										"error":      err.Error(),
										"returned":   fmt.Sprintf("%v", returned),
										"retriable":  retriable,
										"next_delay_ms": func() int {
											if attempt < maxAttempts {
												return int(delay.Milliseconds())
											}
											return 0
										}(),
									},
								))
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								if !retriable {
									// non-retriable: stamp a cause if we have one and stop this group
									ff.Result.HangupCause = ff.Status
									sendResult(&ff)
									break
								}
								sendResult(&ff)
							} else if ff.Result.Success {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", "fax sent successfully (gateway global-batch)", logrus.InfoLevel,
									map[string]interface{}{
										"uuid":       f.UUID.String(),
										"call_uuid":  ff.CallUUID.String(),
										"priority":   prio,
										"attempt":    attempt,
										"elapsed_ms": int(elapsed.Milliseconds()),
									},
								))
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = true
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								sendResult(&ff)
								success = true
								break
							} else {
								// explicit FS failure
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", "fax failed (gateway global-batch explicit failure)", logrus.WarnLevel,
									map[string]interface{}{
										"uuid":         f.UUID.String(),
										"call_uuid":    ff.CallUUID.String(),
										"priority":     prio,
										"attempt":      attempt,
										"elapsed_ms":   int(elapsed.Milliseconds()),
										"result_text":  ff.Result.ResultText,
										"hangup_cause": ff.Result.HangupCause,
										"next_delay_ms": func() int {
											if attempt < maxAttempts {
												return int(delay.Milliseconds())
											}
											return 0
										}(),
									},
								))
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "gateway batch failure"
								ff.Result.HangupCause = ""
								sendResult(&ff)
							}

							if attempt < maxAttempts {
								q.server.FaxTracker.MarkWaiting(f.UUID)
								time.Sleep(delay)
								delay *= 2
							}
						}

						if !success {
							groupHadFailure = true
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue", "gateway global-batch group exhausted without success", logrus.ErrorLevel,
								map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "group_size": len(group)},
							))
						}
					} else {
						// Strategy B: per-endpoint (track individually)
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue", "gateway strategy: per-endpoint (split & track individually)", logrus.InfoLevel,
							map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "group_size": len(group)},
						))

						for _, ep := range group {
							ff := *f
							ff.Endpoints = []*Endpoint{ep}
							ff.Result = &gofaxlib.FaxResult{}

							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue", "processing single gateway endpoint", logrus.InfoLevel,
								map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "endpoint": endpointSummary(ep)},
							))

							success := false
							delay := baseDelay

							for attempt := 1; attempt <= maxAttempts; attempt++ {
								ff.Result = &gofaxlib.FaxResult{}
								ff.CallUUID = uuid.New()
								epType, epLabel, epVal := endpointBrief(ep) // for single-endpoint case
								q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
								q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epType, epLabel, epVal)

								startTime := time.Now()
								returned, err := q.server.FsSocket.SendFax(&ff)
								elapsed := time.Since(startTime)

								if err != nil {
									retriable := (returned == SendRetry)
									level := map[bool]logrus.Level{true: logrus.WarnLevel, false: logrus.ErrorLevel}[retriable]
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue", "fax send error (gateway single)", level,
										map[string]interface{}{
											"uuid":       f.UUID.String(),
											"call_uuid":  ff.CallUUID.String(),
											"priority":   prio,
											"attempt":    attempt,
											"elapsed_ms": int(elapsed.Milliseconds()),
											"error":      err.Error(),
											"returned":   fmt.Sprintf("%v", returned),
											"retriable":  retriable,
											"endpoint":   endpointSummary(ep),
											"next_delay_ms": func() int {
												if attempt < maxAttempts {
													return int(delay.Milliseconds())
												}
												return 0
											}(),
										},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = false
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									if !retriable {
										ff.Result.HangupCause = ff.Status
										sendResult(&ff)
										break
									}
									sendResult(&ff)
								} else if ff.Result.Success {
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue", "fax sent successfully (gateway single)", logrus.InfoLevel,
										map[string]interface{}{
											"uuid":       f.UUID.String(),
											"call_uuid":  ff.CallUUID.String(),
											"priority":   prio,
											"attempt":    attempt,
											"elapsed_ms": int(elapsed.Milliseconds()),
											"endpoint":   endpointSummary(ep),
										},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = true
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									sendResult(&ff)
									success = true
									break
								} else {
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue", "fax failed (gateway single explicit failure)", logrus.WarnLevel,
										map[string]interface{}{
											"uuid":         f.UUID.String(),
											"call_uuid":    ff.CallUUID.String(),
											"priority":     prio,
											"attempt":      attempt,
											"elapsed_ms":   int(elapsed.Milliseconds()),
											"result_text":  ff.Result.ResultText,
											"hangup_cause": ff.Result.HangupCause,
											"endpoint":     endpointSummary(ep),
											"next_delay_ms": func() int {
												if attempt < maxAttempts {
													return int(delay.Milliseconds())
												}
												return 0
											}(),
										},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = false
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									ff.Status = "gateway failure"
									ff.Result.HangupCause = ""
									sendResult(&ff)
								}

								if attempt < maxAttempts {
									q.server.FaxTracker.MarkWaiting(f.UUID)
									time.Sleep(delay)
									delay *= 2
								}
							}

							if !success {
								groupHadFailure = true
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", "endpoint exhausted without success", logrus.ErrorLevel,
									map[string]interface{}{"uuid": f.UUID.String(), "priority": prio, "endpoint": endpointSummary(ep)},
								))
							}
						}
					}

				case "webhook":
					// Prepare shared payload once per priority group.
					var webhookPDFPath, webhookFileB64 string
					{
						pdf, err := tiffToPdf(f.FileName)
						if err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue", fmt.Sprintf("failed to convert tiff to pdf: %v", err), logrus.ErrorLevel,
								map[string]interface{}{"uuid": f.UUID.String()},
							))
						} else {
							webhookPDFPath = pdf
							if b, err := os.ReadFile(pdf); err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", fmt.Sprintf("failed to read fax file for webhook: %v", err), logrus.ErrorLevel,
									map[string]interface{}{"uuid": f.UUID.String()},
								))
							} else {
								webhookFileB64 = base64.StdEncoding.EncodeToString(b)
							}
						}
						// ensure cleanup
						if webhookPDFPath != "" {
							defer func(path string) { _ = os.Remove(path) }(webhookPDFPath)
						}
					}

					for _, ep := range group {
						ff := *f
						ff.Endpoints = []*Endpoint{ep}
						ff.Result = &gofaxlib.FaxResult{}

						success := false
						delay := baseDelay

						for attempt := 1; attempt <= maxAttempts; attempt++ {
							ff.Result = &gofaxlib.FaxResult{}
							startTime := time.Now()

							ff.CallUUID = uuid.New()
							epType, epLabel, epVal := endpointBrief(ep) // for single-endpoint case
							q.server.FaxTracker.SetCall(f.UUID, ff.CallUUID)
							q.server.FaxTracker.MarkAttempt(f.UUID, ff.CallUUID, attempt, maxAttempts, prio, epType, epLabel, epVal)

							if webhookPDFPath == "" || webhookFileB64 == "" {
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "webhook payload not prepared"
								sendResult(&ff)
								break
							}

							payload, err := json.Marshal(FaxJobWithFile{FaxJob: ff, FileData: webhookFileB64})
							if err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", fmt.Sprintf("failed to marshal fax job for webhook: %v", err), logrus.ErrorLevel,
									map[string]interface{}{"uuid": ff.UUID.String()},
								))
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "marshal error"
								sendResult(&ff)
								break
							}

							req, err := http.NewRequest("POST", ep.Endpoint, bytes.NewReader(payload))
							if err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue", fmt.Sprintf("error creating POST request for webhook: %v", err), logrus.ErrorLevel,
									map[string]interface{}{"uuid": ff.UUID.String()},
								))
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "request creation error"
								sendResult(&ff)
							} else {
								req.Header.Set("Content-Type", "application/json")
								resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
								if err != nil {
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue", fmt.Sprintf("error sending POST to webhook (attempt %d): %v", attempt, err), logrus.ErrorLevel,
										map[string]interface{}{"uuid": ff.UUID.String()},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = false
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									ff.Status = "webhook request error"
									sendResult(&ff)
								} else {
									_ = resp.Body.Close()
									if resp.StatusCode >= 200 && resp.StatusCode < 300 {
										ff.Result.UUID = ff.CallUUID
										ff.Result.Success = true
										ff.Result.StartTs = startTime
										ff.Result.EndTs = time.Now()
										ff.Status = fmt.Sprintf("status %d", resp.StatusCode)
										sendResult(&ff)
										success = true
										break
									}
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue", fmt.Sprintf("webhook responded with status %d on attempt %d", resp.StatusCode, attempt), logrus.ErrorLevel,
										map[string]interface{}{"uuid": ff.UUID.String()},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = false
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									ff.Status = fmt.Sprintf("status %d", resp.StatusCode)
									sendResult(&ff)
								}
							}

							if attempt < maxAttempts {
								q.server.FaxTracker.MarkWaiting(f.UUID)
								time.Sleep(delay)
								delay *= 2
							}
						}

						if !success {
							groupHadFailure = true
						}
					}

				default:
					// TODO: add additional endpoint types following the same pattern.
				}

				// If everything in this priority succeeded, STOP escalating.
				if !groupHadFailure {
					break
				}
			}
			// after notify / cleanup, just before return
			q.server.FaxTracker.Complete(f.UUID)
		}(endpointType, prioMap)
	}
	wg.Wait()

	// ---- PREPARE FIRST-PAGE TIFF & NOTIFY DESTS --------------------------------

	// Always remove the main fax file, even on early returns below
	defer func(path string) {
		if err := os.Remove(path); err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue", "failed to remove fax file", logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String()},
			))
		}
	}(f.FileName)

	fpTiff, err := firstPageTiff(f.UUID.String(), f.FileName)
	if err != nil {
		// couldn't generate preview; still return after main file cleanup
		return
	}
	// ensure fpTiff cleanup
	defer func(path string) {
		if err := os.Remove(path); err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue", "failed to remove first page fax file", logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String()},
			))
		}
	}(fpTiff)

	notifyDestinations, err := q.processNotifyDestinations(f)
	if err != nil {
		fmt.Println("Error processing notify destinations:", err)
		return
	}

	q.processNotifyDestinationsAsync(notifyFaxResults, notifyDestinations, fpTiff)
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
