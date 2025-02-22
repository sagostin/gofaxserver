package gofaxserver

import (
	"fmt"
	"github.com/gonicus/gofaxip/gofaxlib/logger"
	"gofaxserver/gofaxlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	fsSocket        *EventSocketServer
	router          *Router
	queue           *Queue
	logManager      *gofaxlib.LogManager
	dialplanManager *DialplanManager
	faxJobRouting   chan *FaxJob
	dB              *gorm.DB
	// In-memory maps for Tenants and TenantNumbers.
	mu            sync.RWMutex
	tenants       map[uint]*Tenant         // keyed by Tenant.ID
	tenantNumbers map[string]*TenantNumber // keyed by the phone number string
	// Endpoints assigned directly to a tenant (keyed by tenant ID).
	tenantEndpoints map[uint][]*Endpoint
	// Endpoints assigned to a specific number (keyed by the phone number string).
	numberEndpoints map[string][]*Endpoint
}

func NewServer() *Server {
	return &Server{faxJobRouting: make(chan *FaxJob),
		tenants:         make(map[uint]*Tenant),
		tenantNumbers:   make(map[string]*TenantNumber),
		tenantEndpoints: make(map[uint][]*Endpoint),
		numberEndpoints: make(map[string][]*Endpoint),
	}
}

func (s *Server) Start() {
	logManager := gofaxlib.NewLogManager(gofaxlib.NewLokiClient())
	logManager.LoadTemplates()
	s.logManager = logManager

	s.dialplanManager = loadDialplan()

	// Shut down receiving lines when killed
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	db, err := gorm.Open(postgres.Open(getPostgresDSN()), &gorm.Config{})
	if err != nil {
		panic(fmt.Errorf("failed to connect to PostgreSQL: %v", err))
	}

	s.dB = db

	// start the router
	queue := NewQueue(s)
	go queue.Start()

	router := NewRouter(s)
	go router.Start()

	s.router = router
	s.queue = queue

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
		fmt.Print("Received ", sig, ", killing all channels")
		time.Sleep(3 * time.Second)
		//logger.Logger.Print("Terminating")
		os.Exit(0)
	}
}

func loadDialplan() *DialplanManager {
	// Define transformation rules:
	rule1 := TransformationRule{
		Pattern:     regexp.MustCompile(`^1(\d{10})$`),
		Replacement: "$1",
	}

	// Rule 2: If the number starts with "001", remove the leading "00"
	rule2 := TransformationRule{
		Pattern:     regexp.MustCompile(`^(\d{10})$`),
		Replacement: "$1",
	}

	// Initialize the DialplanManager with tenant numbers and transformation rules.
	return NewDialplanManager([]TransformationRule{rule1, rule2})
}
