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

type QueueItemStats struct {
	LastEndpoint Endpoint
	Attempts     int // only used for freeswitch gateways, otherwise, we fail and continue to the next endpoint or general notification email
	NextEndpoint Endpoint
}

func (q *Queue) Start() {
	for {
		// these are inbound messages from freeswitch (carrier trunks, vs pbx trunks)
		fax := <-q.Queue

		// process this async?!??
		go func(s *Server, f *FaxJob) {
			// todo send based on endpoints and loop until success/fail with the endpoints based on priority
			// when processing the thing, we only want to send to one or multiple endpoints with the same priority
			// only when it is destined for the default freeswitch gateways, we include multiple

			for _, ep := range f.Endpoints {
				if ep.EndpointType == "gateway" {
					// Create a shallow copy of f.
					ff := f
					// Now override the Endpoints field so that ff has only this one endpoint.
					ff.Endpoints = []*Endpoint{ep}
					// Now you can process or send ff for this gateway.
					result, err := s.fsSocket.SendFax(*ff)
					if err != nil {
						return
					}
				}
			}

		}(q.Server, fax)
		// determine how to route them
	}
	// todo
}
