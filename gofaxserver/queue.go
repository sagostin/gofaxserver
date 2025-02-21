package gofaxserver

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

func (q *Queue) Start() {
	for {
		// these are inbound messages from freeswitch (carrier trunks, vs pbx trunks)
		fax := <-q.Queue

		// process this async?!??
		go func(s *Server, f *FaxJob) {
			// todo send based on endpoints and loop until success/fail with the endpoints based on priority

		}(q.Server, fax)
		// determine how to route them
	}
	// todo
}
