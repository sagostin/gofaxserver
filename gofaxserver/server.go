package gofaxserver

import "github.com/gonicus/gofaxip/gofaxlib/logger"

type Server struct {
	fsSocket  *EventSocketServer
	endpoints []string
}

func NewServer() *Server {
	return &Server{}
}

func (s *Server) Start() {
	// start freeswitch inbound event socket server
	fsSocket := NewEventSocketServer()
	fsSocket.Start()
	go func() {
		select {
		case err := <-fsSocket.Errors():
			logger.Logger.Fatal(err)
		}
	}()
	s.fsSocket = fsSocket

	// start web server
	// todo

	// start loki logger channel / client
	// todo
}
