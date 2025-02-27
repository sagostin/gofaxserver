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

// processFax processes each endpoint type concurrently.
// For each endpoint type, it iterates over its priority groups (with priority 999 always last)
// and sends the fax using a copy of the fax job (ff) for that group. For "gateway" endpoints,
// it retries up to three times before sending the final result.
func (q *Queue) processFax(f *FaxJob) {
	// Build a nested map: groupMap[endpointType][priority] = []*Endpoint
	groupMap := make(map[string]map[uint][]*Endpoint)
	for _, ep := range f.Endpoints {
		if groupMap[ep.EndpointType] == nil {
			groupMap[ep.EndpointType] = make(map[uint][]*Endpoint)
		}
		groupMap[ep.EndpointType][ep.Priority] = append(groupMap[ep.EndpointType][ep.Priority], ep)
	}

	var wg sync.WaitGroup
	for endpointType, prioMap := range groupMap {
		wg.Add(1)
		go func(epType string, prioMap map[uint][]*Endpoint) {
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

			// todo -> there should never be multiple gateway endpoints with the same priority
			// todo -> unless it is the default gateway ones, otherwise send each at the same time but simultaneously
			// Process each priority group sequentially.
			for _, prio := range prios {
				group := prioMap[prio]
				// Create a shallow copy of the fax job for this group.
				ff := *f
				ff.Endpoints = group

				// Helper to report an attempt.
				sendResult := func() {
					q.QueueFaxResult <- QueueFaxResult{
						Job: &ff,
					}
				}

				ff.Result = &gofaxlib.FaxResult{}

				switch epType {
				case "gateway":
					const maxAttempts = 3    // todo max attempts config
					delay := 2 * time.Second // todo config delay
					for attempt := 1; attempt <= maxAttempts; attempt++ {
						_, err := q.server.FsSocket.SendFax(&ff)
						// Always update the job pointer before reporting.
						if err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								"error sending fax (gateway) attempt %d, priority %d, callee: %s, caller: %s, err: %v, endpoints: %v",
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
								attempt, prio, ff.CalleeNumber, ff.CallerIdName, err, ff.Endpoints,
							))
							ff.Result.Success = false
							sendResult()
						} else if ff.Result.Success {
							sendResult()
							break
						} else {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								"attempt %d (gateway) failed: priority %d, callee: %s, caller: %s, result: %v, endpoints: %v",
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
								attempt, prio, ff.CalleeNumber, ff.CallerIdName, ff.Result.ResultText, ff.Endpoints,
							))
							ff.Result.Success = false
							sendResult()
						}
						time.Sleep(delay)
						delay *= 2
					}
				case "webhook":
					// We'll attempt to send the fax job as a JSON payload to the webhook URL.
					// Before marshalling, read the file from disk and embed its contents.
					fileBytes, err := os.ReadFile(ff.FileName)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							fmt.Sprintf("failed to read fax file for webhook: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": ff.UUID.String()},
						))
						ff.Result.Success = false
						ff.Status = "failed to read fax file"
						sendResult()
						break
					}
					fileData := base64.StdEncoding.EncodeToString(fileBytes)

					// Create an augmented struct that embeds the fax job and includes the file data.
					faxJobWithFile := FaxJobWithFile{
						FaxJob:   ff,
						FileData: fileData,
					}
					const maxAttempts = 3
					delay := 2 * time.Second
					for attempt := 1; attempt <= maxAttempts; attempt++ {
						ff.CallUUID = uuid.New()

						payload, err := json.Marshal(faxJobWithFile)
						if err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								fmt.Sprintf("failed to marshal fax job for webhook: %v", err),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
							))
							ff.Result.Success = false
							ff.Status = "failed to marshal fax job"
							sendResult()
							break
						}

						// Use the first endpoint's URL as the webhook URL.
						webhookURL := ff.Endpoints[0].Endpoint
						req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(payload))
						if err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								fmt.Sprintf("error creating POST request for webhook: %v", err),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
							))
							ff.Result.Success = false
							ff.Status = "failed to create request"
							sendResult()
						} else {
							req.Header.Set("Content-Type", "application/json")
							client := &http.Client{Timeout: 10 * time.Second}
							resp, err := client.Do(req)
							if err != nil {
								q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
									"Queue",
									fmt.Sprintf("error sending POST request to webhook (attempt %d): %v", attempt, err),
									logrus.ErrorLevel,
									map[string]interface{}{"uuid": ff.UUID.String()},
								))
								ff.Result.Success = false
								ff.Status = "webhook request error"
								sendResult()
							} else {
								resp.Body.Close()
								if resp.StatusCode >= 200 && resp.StatusCode < 300 {
									ff.Result.Success = true
									ff.Result.HangupCause = strconv.Itoa(resp.StatusCode)
									ff.Status = fmt.Sprintf("webhook responded with status %d", resp.StatusCode)
									sendResult()
									break
								} else {
									q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
										"Queue",
										fmt.Sprintf("webhook responded with status %d on attempt %d", resp.StatusCode, attempt),
										logrus.ErrorLevel,
										map[string]interface{}{"uuid": ff.UUID.String()},
									))
									ff.Result.Success = false
									ff.Result.HangupCause = strconv.Itoa(resp.StatusCode)
									ff.Status = fmt.Sprintf("webhook error: status %d", resp.StatusCode)
									sendResult()
								}
							}
						}
						time.Sleep(delay)
						delay *= 2
					}
				default:
					// Handle additional endpoint types here.
				}
			}
		}(endpointType, prioMap)
	}
	wg.Wait()

	// todo remove the file after processing
	err := os.Remove(f.FileName)
	if err != nil {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
			"Queue",
			"failed to remove fax file",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": f.UUID.String()},
		))
		return
	}
}
