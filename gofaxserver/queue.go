package gofaxserver

import (
	"fmt"
	"sort"
	"time"
)

// sortEndpoints sorts endpoints so that non‑666 priorities come first (ascending),
// and endpoints with priority 666 are grouped at the end.
func sortEndpoints(eps []*Endpoint) {
	sort.Slice(eps, func(i, j int) bool {
		a := eps[i].Priority
		b := eps[j].Priority
		// If one is 666 and the other is not, the one that is not 666 comes first.
		if a == 666 && b != 666 {
			return false
		} else if b == 666 && a != 666 {
			return true
		}
		// Otherwise, sort in ascending order.
		return a < b
	})
}

// groupEndpoints groups a sorted slice of endpoints by their priority.
// Each group is a slice of endpoints sharing the same Priority.
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

// Queue represents a fax job processing queue.
type Queue struct {
	Queue  chan *FaxJob
	Server *Server
}

func NewQueue(s *Server) *Queue {
	return &Queue{
		Queue:  make(chan *FaxJob),
		Server: s,
	}
}

// Start processes fax jobs from the queue.
func (q *Queue) Start() {
	for {
		fax := <-q.Queue
		// Process each fax asynchronously.
		go q.processFax(fax)
	}
}

// processFax groups endpoints by type and then by priority,
// then processes each group according to the rules described.
func (q *Queue) processFax(f *FaxJob) {
	// Build a nested map:
	// groupMap[endpointType][priority] = []*Endpoint
	groupMap := make(map[string]map[uint][]*Endpoint)
	for _, ep := range f.Endpoints {
		if groupMap[ep.EndpointType] == nil {
			groupMap[ep.EndpointType] = make(map[uint][]*Endpoint)
		}
		groupMap[ep.EndpointType][ep.Priority] = append(groupMap[ep.EndpointType][ep.Priority], ep)
	}

	// Process each endpoint type independently.
	for endpointType, prioMap := range groupMap {
		// Extract and sort priorities.
		var prios []uint
		for prio := range prioMap {
			prios = append(prios, prio)
		}
		// Sort ascending, except that priority 999 always goes last.
		sort.Slice(prios, func(i, j int) bool {
			a, b := prios[i], prios[j]
			if a == 999 && b != 999 {
				return false
			} else if b == 999 && a != 999 {
				return true
			}
			return a < b
		})

		// Process groups in order.
		for _, prio := range prios {
			group := prioMap[prio]
			// Make a shallow copy of the fax job and assign the current group as its endpoints.
			ff := *f
			ff.Endpoints = group

			switch endpointType {
			case "gateway":
				// For gateways, we use retries if the priority is not 666.
				// For non-default gateway endpoints, retry the entire group a configurable number of times.
				const maxAttempts = 3
				delay := 2 * time.Second
				success := false
				for attempt := 1; attempt <= maxAttempts; attempt++ {
					_, err := q.Server.FsSocket.SendFax(ff)
					if err != nil {
						fmt.Printf("Attempt %d: Error sending fax via gateway group (priority %d): %v\n", attempt, prio, err)
					} else if f.Result.Success {
						fmt.Printf("Fax sent successfully via gateway group (priority %d) on attempt %d\n", prio, attempt)
						success = true
						break
					} else {
						fmt.Printf("Attempt %d: Fax send failed via gateway group (priority %d), result: %s\n", attempt, prio, f.Result.ResultText)
					}
					time.Sleep(delay)
					// Optionally increase delay for the next attempt.
					delay = delay * 2
				}
				if success {
					return // fax sent – exit processing
				} else {
					// After exhausting attempts, you might notify the contact on file.
					fmt.Printf("All attempts failed for gateway group (priority %d). Notifying contact...\n", prio)
					// Notification logic here...
				}
			default:
				// For other endpoint types (e.g. "webhook"), send concurrently (without retries).
				go func(job FaxJob, etype string, prio uint) {
					_, err := q.Server.FsSocket.SendFax(job)
					if err != nil {
						fmt.Printf("Error sending fax via %s group (priority %d): %v\n", etype, prio, err)
					} else if f.Result.Success {
						fmt.Printf("Fax sent successfully via %s group (priority %d)\n", etype, prio)
					} else {
						fmt.Printf("Fax send failed via %s group (priority %d), result: %s\n", etype, prio, f.Result.ResultText)
					}
				}(ff, endpointType, prio)
			}
		}
	}
	// If none of the endpoint groups succeeded, handle overall failure.
	fmt.Printf("All endpoint attempts failed for fax job %v\n", f.UUID)
}
