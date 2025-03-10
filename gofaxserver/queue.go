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

			maxAttempts := 3
			if gofaxlib.Config.Faxing.RetryAttempts == "" {
				rMax, err := strconv.Atoi(gofaxlib.Config.Faxing.RetryAttempts)
				if err == nil {
					maxAttempts = rMax
				}
			}
			delay := 1 * time.Minute

			dD, err := ParseDuration(gofaxlib.Config.Faxing.RetryDelay)
			if err == nil {
				delay = dD
			}

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
			for _, prio := range prios {
				group := prioMap[prio]
				// Create a shallow copy of the fax job for this group.
				ff := *f
				ff.Endpoints = group
				// Initialize Result.
				ff.Result = &gofaxlib.FaxResult{}

				// Helper to report an attempt.
				sendResult := func() {
					q.QueueFaxResult <- QueueFaxResult{Job: &ff}

					copyFax := ff // copy the current attempt state
					nFR.Results[ff.CallUUID.String()] = &copyFax
				}

				switch epType {
				case "gateway":
					for attempt := 1; attempt <= maxAttempts; attempt++ {
						ff.Result = &gofaxlib.FaxResult{}
						ff.CallUUID = uuid.New()
						startTime := time.Now()
						returned, err := q.server.FsSocket.SendFax(&ff)
						if err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								fmt.Sprintf("error sending fax (gateway) attempt %d, priority %d, callee: %s, caller: %s, err: %v, endpoints: %v",
									attempt, prio, ff.CalleeNumber, ff.CallerIdName, err, ff.Endpoints),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
							))
							ff.Result.UUID = ff.CallUUID
							ff.Result.Success = false
							ff.Result.StartTs = startTime
							ff.Result.EndTs = time.Now()

							if returned != SendRetry {
								ff.Result.HangupCause = ff.Status
								attempt = maxAttempts
							}
							sendResult()
						} else if ff.Result.Success {
							ff.Result.UUID = ff.CallUUID
							ff.Result.Success = true
							sendResult()
							break
						} else {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Queue",
								fmt.Sprintf("attempt %d (gateway) failed: priority %d, callee: %s, caller: %s, result: %v, endpoints: %v",
									attempt, prio, ff.CalleeNumber, ff.CallerIdName, ff.Result.ResultText, ff.Endpoints),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": ff.UUID.String()},
							))
							ff.Result.UUID = ff.CallUUID
							ff.Result.Success = false
							ff.Result.StartTs = startTime
							ff.Result.EndTs = time.Now()
							ff.Status = "gateway failure"
							ff.Result.HangupCause = ""
							sendResult()
						}
						if attempt >= maxAttempts {
							break
						}
						time.Sleep(delay)
						delay *= 2
					}
				case "webhook":
					// Read the file from disk.

					// todo convert to pdf for sending to webhook, so we can assume pdf
					pdf, err := tiffToPdf(ff.FileName)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							fmt.Sprintf("failed to convert tiff to pdf: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": ff.UUID.String()},
						))
					}

					fileBytes, err := os.ReadFile(pdf)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Queue",
							fmt.Sprintf("failed to read fax file for webhook: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": ff.UUID.String()},
						))
						ff.Result.UUID = ff.CallUUID
						ff.Result.Success = false
						ff.Result.StartTs = time.Now()
						ff.Result.EndTs = time.Now()
						ff.Status = fmt.Sprintf("failed to read fax file: %v", err)
						ff.Result.HangupCause = ""
						sendResult()
						break
					}

					fileData := base64.StdEncoding.EncodeToString(fileBytes)

					for attempt := 1; attempt <= maxAttempts; attempt++ {
						ff.Result = &gofaxlib.FaxResult{}
						ff.CallUUID = uuid.New()
						startTime := time.Now()

						faxJobWithFile := FaxJobWithFile{
							FaxJob:   ff,
							FileData: fileData,
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
							ff.Result.HangupCause = ""
							sendResult()
							break
						}
						webhookURL := ff.Endpoints[0].Endpoint
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
							ff.Result.HangupCause = ""
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
								ff.Result.UUID = ff.CallUUID
								ff.Result.Success = false
								ff.Result.StartTs = startTime
								ff.Result.EndTs = time.Now()
								ff.Status = "webhook request error"
								ff.Result.HangupCause = ""
								sendResult()
							} else {
								resp.Body.Close()
								if resp.StatusCode >= 200 && resp.StatusCode < 300 {
									ff.Result.UUID = ff.CallUUID
									ff.Result.Success = true
									ff.Result.StartTs = startTime
									ff.Result.EndTs = time.Now()
									ff.Status = fmt.Sprintf("status %d", resp.StatusCode)
									ff.Result.HangupCause = ""
									sendResult()
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
									ff.Result.HangupCause = ""
									sendResult()
								}
							}
						}
						if attempt >= maxAttempts {
							break
						}
						time.Sleep(delay)
						delay *= 2
					}

					os.Remove(pdf) // remove temp pdf file that we converted from tiff
				default:
					// Handle additional endpoint types if needed.
				}
			}
		}(&notifyFaxResults, endpointType, prioMap)
	}
	wg.Wait()

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

	// notify of the results

	// Generate a PDF report with the fax results.
	/*pdfPath, err := notifyFaxResults.GenerateFaxResultsPDF()
	if err != nil {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
			"Queue",
			"failed to save fax result report",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": f.UUID.String(), "pdf_path": pdfPath},
		))
		return
	}*/

	notifyDestinations, err := q.processNotifyDestinations(f)
	if err != nil {
		fmt.Println("Error processing notify destinations:", err)
		return
	}

	q.processNotifyDestinationsAsync(notifyFaxResults, notifyDestinations)

}
