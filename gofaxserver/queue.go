package gofaxserver

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// sortEndpoints sorts endpoints so that non‑666 priorities come first (ascending),
// and endpoints with priority 666 are grouped at the end.
func sortEndpoints(eps []*Endpoint) {
	sort.Slice(eps, func(i, j int) bool {
		a := eps[i].Priority
		b := eps[j].Priority
		if a == 666 && b != 666 {
			return false
		} else if b == 666 && a != 666 {
			return true
		}
		return a < b
	})
}

// groupEndpoints groups a sorted slice of endpoints by their priority.
func groupEndpoints(eps []*Endpoint) [][]*Endpoint {
	var groups [][]*Endpoint
	i := 0
	for i < len(eps) {
		currentPriority := eps[i].Priority
		var group []*Endpoint
		for i < len(eps) && eps[i].Priority == currentPriority {
			group = append(group, eps[i])
			i++
		}
		groups = append(groups, group)
	}
	return groups
}

// QueueFaxResult holds the result of a fax send attempt for a specific endpoint group.
type QueueFaxResult struct {
	Job      *FaxJob     `json:"job"`
	Success  bool        `json:"success"`
	Response interface{} `json:"response"`
	Err      error       `json:"error,omitempty"`
}

// Queue represents a fax job processing queue.
type Queue struct {
	Queue          chan *FaxJob
	Server         *Server
	QueueFaxResult chan QueueFaxResult
}

// NewQueue creates a new Queue.
func NewQueue(s *Server) *Queue {
	return &Queue{
		Queue:          make(chan *FaxJob),
		QueueFaxResult: make(chan QueueFaxResult),
		Server:         s,
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

// processFax processes each endpoint type concurrently. For each endpoint type, it iterates
// over its priority groups (ordered so that 999 comes last) and sends the fax using the endpoints
// in that group. A shallow copy (ff) of the original fax job is made per group and reused for retries,
// so that modifications made by SendFax persist.
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

	// Process each endpoint type concurrently.
	for endpointType, prioMap := range groupMap {
		wg.Add(1)
		go func(endpointType string, prioMap map[uint][]*Endpoint) {
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
			for _, prio := range prios {
				group := prioMap[prio]
				// Create one copy of the fax job for this group.
				ff := *f
				ff.Endpoints = group

				var result QueueFaxResult

				switch endpointType {
				case "gateway":
					// For gateways, retry up to three times.
					const maxAttempts = 3
					delay := 2 * time.Second
					for attempt := 1; attempt <= maxAttempts; attempt++ {
						ret, err := q.Server.FsSocket.SendFax(&ff)
						if err != nil {
							fmt.Printf("Error sending fax (gateway, priority %d, attempt %d): %v\n", prio, attempt, err)
						} else if ff.Result.Success {
							result.Success = true
							result.Response = ret
							result.Job = &ff
							break
						} else {
							fmt.Printf("Attempt %d: Fax send failed via gateway (priority %d), result: %s\n", attempt, prio, ff.Result.ResultText)
						}
						time.Sleep(delay)
						delay *= 2
					}
					if !result.Success {
						result.Response = ff.Result.ResultText
					}
				default:
					// Uncomment and implement for additional endpoint types if needed.
					/*
						ret, err := q.Server.FsSocket.SendFax(&ff)
						if err != nil {
							result.Err = err
							fmt.Printf("Error sending fax via %s (priority %d): %v\n", endpointType, prio, err)
						} else if ff.Result.Success {
							result.Success = true
							result.Response = ret
						} else {
							result.Response = ff.Result.ResultText
							fmt.Printf("Fax send failed via %s (priority %d), result: %s\n", endpointType, prio, ff.Result.ResultText)
						}
					*/
				}
				// Send the result for this endpoint group to the global results channel.
				q.QueueFaxResult <- result
			}
		}(endpointType, prioMap)
	}

	// Wait for all endpoint type goroutines to finish.
	wg.Wait()
}

// startQueueResults processes results from the QueueFaxResult channel asynchronously.
// In the future, you can add logic here to persist results to a database, etc.
func (q *Queue) startQueueResults() {
	for result := range q.QueueFaxResult {
		// TODO: Process each result asynchronously (e.g., add to DB).
		fmt.Printf("Processing result for job %v: %+v\n", result.Job.UUID, result)
	}
}
