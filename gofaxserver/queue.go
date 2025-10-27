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
	// Build a nested map: groupMap[endpointType][priority] = []*Endpoint.
	groupMap := make(map[string]map[uint][]*Endpoint)
	for _, ep := range f.Endpoints {
		if groupMap[ep.EndpointType] == nil {
			groupMap[ep.EndpointType] = make(map[uint][]*Endpoint)
		}
		groupMap[ep.EndpointType][ep.Priority] = append(groupMap[ep.EndpointType][ep.Priority], ep)
	}

	// Use a results map that holds pointers so updates persist.
	notifyFaxResults := NotifyFaxResults{
		Results: make(map[string]*FaxJob),
		FaxJob:  f,
	}

	var wg sync.WaitGroup
	for endpointType, prioMap := range groupMap {
		wg.Add(1)
		go func(nFR *NotifyFaxResults, epType string, prioMap map[uint][]*Endpoint) {
			defer wg.Done()

			// Extract and sort priorities (with 999 always last).
			var prios []uint
			for prio := range prioMap {
				prios = append(prios, prio)
			}

			sort.Slice(prios, func(i, j int) bool {
				a, b := prios[i], prios[j]
				if a == 999 && b != 999 {
					return false
				} else if b == 999 && a != 999 {
					return true
				}
				return a < b
			})

			// Process each priority group sequentially.
			// Process each priority group sequentially with "advance-on-any-failure" semantics.
			for _, prio := range prios {
				group := prioMap[prio]

				// Parse retry config (fix: only parse if non-empty)
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

				// Helper to record/send an attempt result and snapshot into notify map.
				sendResult := func(ff *FaxJob) {
					q.QueueFaxResult <- QueueFaxResult{Job: ff}
					copyFax := *ff // snapshot state
					notifyFaxResults.Results[ff.CallUUID.String()] = &copyFax
				}

				// Precompute webhook payload once per priority group (if any ep is webhook)
				var webhookPDFPath string
				var webhookFileB64 string
				var webhookPrepared bool
				prepareWebhookPayload := func(ffBase *FaxJob) {
					if webhookPrepared {
						return
					}
					// Convert (once) and read file for this priority group
					pdf, err := tiffToPdf(ffBase.FileName)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							fmt.Sprintf("failed to convert tiff to pdf: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": ffBase.UUID.String()},
						))
						webhookPrepared = true // mark as prepared to avoid reattempting
						return
					}
					webhookPDFPath = pdf

					fileBytes, err := os.ReadFile(pdf)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							fmt.Sprintf("failed to read fax file for webhook: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": ffBase.UUID.String()},
						))
						webhookPrepared = true
						return
					}
					webhookFileB64 = base64.StdEncoding.EncodeToString(fileBytes)
					webhookPrepared = true
				}

				// Process endpoints within this priority; advance to next priority only if ANY failed fully.
				groupHadFailure := false

				switch endpointType {
				case "gateway":
					// strategy banner for the priority group
					{
						gws := make([]string, 0, len(group))
						for _, e := range group {
							gws = append(gws, gatewayLabel(e))
						}
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							"processing gateway group",
							logrus.InfoLevel,
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

					// If ALL endpoints in this priority group are global upstream gateways,
					// keep the list intact and let FreeSWITCH handle sequencing/failover.
					if isGlobalUpstreamGatewayGroup(group) {
						ff := *f
						ff.Endpoints = group // keep full list
						ff.Result = &gofaxlib.FaxResult{}
						success := false
						delay := baseDelay

						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							"gateway strategy: global-batch (FreeSWITCH handles fan-out/failover)",
							logrus.InfoLevel,
							map[string]interface{}{
								"uuid":       f.UUID.String(),
								"priority":   prio,
								"group_size": len(group),
							},
						))

						for attempt := 1; attempt <= maxAttempts; attempt++ {
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()
							startTime := time.Now()
							nextDelay := 0 * time.Second
							if attempt < maxAttempts {
								nextDelay = delay
							}

							// attempt-start log
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								"sending fax (gateway global-batch) attempt start",
								logrus.InfoLevel,
								map[string]interface{}{
									"uuid":          f.UUID.String(),
									"call_uuid":     ff.CallUUID.String(),
									"priority":      prio,
									"attempt":       attempt,
									"max_attempts":  maxAttempts,
									"callee":        ff.CalleeNumber,
									"caller":        ff.CallerIdName,
									"group_size":    len(group),
									"next_delay_ms": int(nextDelay.Milliseconds()),
								},
							))

							returned, err := q.server.FsSocket.SendFax(&ff)
							elapsed := time.Since(startTime)

							if err != nil {
								// error from send path
								level := logrus.WarnLevel
								retriable := (returned == SendRetry)
								if !retriable {
									level = logrus.ErrorLevel
								}
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									"fax send error (gateway global-batch)",
									level,
									map[string]interface{}{
										"uuid":          f.UUID.String(),
										"call_uuid":     ff.CallUUID.String(),
										"priority":      prio,
										"attempt":       attempt,
										"elapsed_ms":    int(elapsed.Milliseconds()),
										"error":         err.Error(),
										"returned":      fmt.Sprintf("%v", returned),
										"retriable":     retriable,
										"next_delay_ms": int(nextDelay.Milliseconds()),
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
								// success
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									"fax sent successfully (gateway global-batch)",
									logrus.InfoLevel,
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
								// explicit failure from FS with batch dialstring
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									"fax failed (gateway global-batch explicit failure)",
									logrus.WarnLevel,
									map[string]interface{}{
										"uuid":          f.UUID.String(),
										"call_uuid":     ff.CallUUID.String(),
										"priority":      prio,
										"attempt":       attempt,
										"elapsed_ms":    int(elapsed.Milliseconds()),
										"result_text":   ff.Result.ResultText,
										"hangup_cause":  ff.Result.HangupCause,
										"next_delay_ms": int(nextDelay.Milliseconds()),
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
								time.Sleep(delay)
								delay *= 2
							}
						}

						if !success {
							groupHadFailure = true
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								"gateway global-batch group exhausted without success",
								logrus.ErrorLevel,
								map[string]interface{}{
									"uuid":       f.UUID.String(),
									"priority":   prio,
									"group_size": len(group),
								},
							))
						}
					} else {
						// Default behavior: split per-endpoint so we can track failures individually
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							"gateway strategy: per-endpoint (split & track individually)",
							logrus.InfoLevel,
							map[string]interface{}{
								"uuid":       f.UUID.String(),
								"priority":   prio,
								"group_size": len(group),
							},
						))

						for _, ep := range group {
							ff := *f
							ff.Endpoints = []*Endpoint{ep}
							ff.Result = &gofaxlib.FaxResult{}

							success := false
							delay := baseDelay

							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								"processing single gateway endpoint",
								logrus.InfoLevel,
								map[string]interface{}{
									"uuid":     f.UUID.String(),
									"priority": prio,
									"endpoint": endpointSummary(ep),
								},
							))

							for attempt := 1; attempt <= maxAttempts; attempt++ {
								ff.Result = &gofaxlib.FaxResult{}
								ff.CallUUID = uuid.New()
								startTime := time.Now()
								nextDelay := 0 * time.Second
								if attempt < maxAttempts {
									nextDelay = delay
								}

								// attempt-start log
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									"sending fax (gateway single) attempt start",
									logrus.InfoLevel,
									map[string]interface{}{
										"uuid":          f.UUID.String(),
										"call_uuid":     ff.CallUUID.String(),
										"priority":      prio,
										"attempt":       attempt,
										"max_attempts":  maxAttempts,
										"callee":        ff.CalleeNumber,
										"caller":        ff.CallerIdName,
										"endpoint":      endpointSummary(ep),
										"next_delay_ms": int(nextDelay.Milliseconds()),
									},
								))

								returned, err := q.server.FsSocket.SendFax(&ff)
								elapsed := time.Since(startTime)

								if err != nil {
									retriable := (returned == SendRetry)
									level := logrus.WarnLevel
									if !retriable {
										level = logrus.ErrorLevel
									}
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue",
										"fax send error (gateway single)",
										level,
										map[string]interface{}{
											"uuid":          f.UUID.String(),
											"call_uuid":     ff.CallUUID.String(),
											"priority":      prio,
											"attempt":       attempt,
											"elapsed_ms":    int(elapsed.Milliseconds()),
											"error":         err.Error(),
											"returned":      fmt.Sprintf("%v", returned),
											"retriable":     retriable,
											"endpoint":      endpointSummary(ep),
											"next_delay_ms": int(nextDelay.Milliseconds()),
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
										"Queue",
										"fax sent successfully (gateway single)",
										logrus.InfoLevel,
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
										"Queue",
										"fax failed (gateway single explicit failure)",
										logrus.WarnLevel,
										map[string]interface{}{
											"uuid":          f.UUID.String(),
											"call_uuid":     ff.CallUUID.String(),
											"priority":      prio,
											"attempt":       attempt,
											"elapsed_ms":    int(elapsed.Milliseconds()),
											"result_text":   ff.Result.ResultText,
											"hangup_cause":  ff.Result.HangupCause,
											"endpoint":      endpointSummary(ep),
											"next_delay_ms": int(nextDelay.Milliseconds()),
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
									time.Sleep(delay)
									delay *= 2
								}
							}

							if !success {
								groupHadFailure = true
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									"endpoint exhausted without success",
									logrus.ErrorLevel,
									map[string]interface{}{
										"uuid":     f.UUID.String(),
										"priority": prio,
										"endpoint": endpointSummary(ep),
									},
								))
							}
						}
					}

				case "webhook":
					// Prepare shared payload once per priority group
					prepareWebhookPayload(f)

					for _, ep := range group {
						ff := *f
						ff.Endpoints = []*Endpoint{ep}
						ff.Result = &gofaxlib.FaxResult{}

						success := false
						delay := baseDelay

						for attempt := 1; attempt <= maxAttempts; attempt++ {
							ff.Result = &gofaxlib.FaxResult{}
							ff.CallUUID = uuid.New()
							startTime := time.Now()

							// If we couldn't prepare a payload, fail immediately
							if webhookPDFPath == "" || webhookFileB64 == "" {
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "webhook payload not prepared"
								sendResult(&ff)
								break
							}

							// Build payload
							faxJobWithFile := FaxJobWithFile{
								FaxJob:   ff,
								FileData: webhookFileB64,
							}
							payload, err := json.Marshal(faxJobWithFile)
							if err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									fmt.Sprintf("failed to marshal fax job for webhook: %v", err),
									logrus.ErrorLevel,
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

							webhookURL := ep.Endpoint // per-endpoint
							req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(payload))
							if err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									fmt.Sprintf("error creating POST request for webhook: %v", err),
									logrus.ErrorLevel,
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
								client := &http.Client{Timeout: 10 * time.Second}
								resp, err := client.Do(req)
								if err != nil {
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue",
										fmt.Sprintf("error sending POST to webhook (attempt %d): %v", attempt, err),
										logrus.ErrorLevel,
										map[string]interface{}{"uuid": ff.UUID.String()},
									))
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = false
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									ff.Status = "webhook request error"
									sendResult(&ff)
								} else {
									resp.Body.Close()
									if resp.StatusCode >= 200 && resp.StatusCode < 300 {
										ff.Result.UUID = ff.CallUUID
										ff.Result.Success = true
										ff.Result.StartTs = startTime
										ff.Result.EndTs = time.Now()
										ff.Status = fmt.Sprintf("status %d", resp.StatusCode)
										sendResult(&ff)
										success = true
										break
									} else {
										q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
											"Queue",
											fmt.Sprintf("webhook responded with status %d on attempt %d", resp.StatusCode, attempt),
											logrus.ErrorLevel,
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
							}

							if attempt < maxAttempts {
								time.Sleep(delay)
								delay *= 2
							}
						}

						if !success {
							groupHadFailure = true
						}
					}

					// Clean up the shared PDF for this priority group
					if webhookPDFPath != "" {
						_ = os.Remove(webhookPDFPath)
					}

				default:
					// Handle additional endpoint types if needed in the same pattern:
					// - per-endpoint retries
					// - mark groupHadFailure if any endpoint ultimately fails
				}

				// DECISION: advance to higher priority only if ANY endpoint in this priority failed.
				if !groupHadFailure {
					// All endpoints in this priority ultimately succeeded → stop here.
					break
				}
			}
		}(&notifyFaxResults, endpointType, prioMap)
	}
	wg.Wait()

	fpTiff, err := firstPageTiff(f.UUID.String(), f.FileName)
	if err != nil {
		return
	}

	notifyDestinations, err := q.processNotifyDestinations(f)
	if err != nil {
		fmt.Println("Error processing notify destinations:", err)
		return
	}

	q.processNotifyDestinationsAsync(notifyFaxResults, notifyDestinations, fpTiff)

	defer func() {
		// Remove the fax file after processing.
		if err := os.Remove(f.FileName); err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue",
				"failed to remove fax file",
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String()},
			))
			return
		}

		err := os.Remove(fpTiff)
		if err != nil {
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"Queue",
				"failed to remove first page fax file",
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": f.UUID.String()},
			))
			return
		}
	}()
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
