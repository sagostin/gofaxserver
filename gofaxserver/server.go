package gofaxserver

import (
	"github.com/gonicus/gofaxip/gofaxlib"
	"github.com/gonicus/gofaxip/gofaxlib/logger"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	fsSocket             *EventSocketServer
	router               *Router
	endpoints            []string
	logManager           *gofaxlib.LogManager
	fsInboundXFRecordCh  chan *gofaxlib.XFRecord
	fsOutboundXFRecordCh chan *gofaxlib.XFRecord
}

func NewServer() *Server {
	return &Server{fsInboundXFRecordCh: make(chan *gofaxlib.XFRecord), fsOutboundXFRecordCh: make(chan *gofaxlib.XFRecord)}
}

func (s *Server) Start() {
	logManager := gofaxlib.NewLogManager(gofaxlib.NewLokiClient())
	logManager.LoadTemplates()
	s.logManager = logManager

	// Shut down receiving lines when killed
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	// start the router
	router := NewRouter(s)
	s.router = router

	// start freeswitch inbound event socket server
	fsSocket := NewEventSocketServer(s)
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

	select {
	case sig := <-sigchan:
		logger.Logger.Print("Received ", sig, ", killing all channels")
		time.Sleep(3 * time.Second)
		logger.Logger.Print("Terminating")
		os.Exit(0)
	}
}
