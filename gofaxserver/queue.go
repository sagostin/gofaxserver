package gofaxserver

type Queue struct {
	Queue chan *FaxJob
}

func NewQueue() *Queue {
	return &Queue{
		Queue: make(chan *FaxJob),
	}
}

func (q *Queue) Start() {
	go func() {
		for {
			// these are inbound messages from freeswitch (carrier trunks, vs pbx trunks)
			inboundMsg := <-q.Queue
			// determine how to route them
		}
		// todo
	}()
}
