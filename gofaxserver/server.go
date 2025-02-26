package gofaxserver

import (
	"fmt"
	"github.com/gonicus/gofaxip/gofaxlib/logger"
	"github.com/joho/godotenv"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
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
	FsSocket        *EventSocketServer   `json:"fs_socket,omitempty"`
	Router          *Router              `json:"router,omitempty"`
	Queue           *Queue               `json:"queue,omitempty"`
	LogManager      *gofaxlib.LogManager `json:"log_manager,omitempty"`
	DialplanManager *DialplanManager     `json:"dialplan_manager,omitempty"`
	FaxJobRouting   chan *FaxJob         `json:"fax_job_routing,omitempty"`
	DB              *gorm.DB             `json:"d_b,omitempty"`
	// In-memory maps for Tenants and TenantNumbers.
	mu            sync.RWMutex
	Tenants       map[uint]*Tenant         `json:"tenants,omitempty"`        // keyed by Tenant.ID
	TenantNumbers map[string]*TenantNumber `json:"tenant_numbers,omitempty"` // keyed by the phone number string
	// Endpoints assigned directly to a tenant (keyed by tenant ID).
	TenantEndpoints map[uint][]*Endpoint `json:"tenant_endpoints,omitempty"`
	// Endpoints assigned to a specific number (keyed by the phone number string).
	NumberEndpoints     map[string][]*Endpoint `json:"number_endpoints,omitempty"`
	GatewayEndpointsACL []string               `json:"gateway_endpoint_acl,omitempty"` // allowed source IPs for SIP trunks
	UpstreamFsGateways  []string               `json:"upstream_fs_gateways"`           // upstream gateway names defined in the global endpoints config/DB
}

func NewServer() *Server {
	return &Server{FaxJobRouting: make(chan *FaxJob),
		Tenants:         make(map[uint]*Tenant),
		TenantNumbers:   make(map[string]*TenantNumber),
		TenantEndpoints: make(map[uint][]*Endpoint),
		NumberEndpoints: make(map[string][]*Endpoint),
	}
}

func (s *Server) Start() {
	fmt.Print("starting gofaxserver")

	gofaxlib.Config.Loki.Job = "faxserver"

	err := godotenv.Load()
	if err != nil {
		return
	}

	logManager := gofaxlib.NewLogManager(gofaxlib.NewLokiClient())
	logManager.LoadTemplates()
	s.LogManager = logManager

	s.DialplanManager = loadDialplan()

	// Shut down receiving lines when killed
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	db, err := gorm.Open(postgres.Open(getPostgresDSN()), &gorm.Config{})
	if err != nil {
		panic(fmt.Errorf("failed to connect to PostgreSQL: %v", err))
	}

	s.DB = db

	err = s.migrateSchema()
	if err != nil {
		return
	}

	// start the router
	queue := NewQueue(s)
	go queue.Start()

	router := NewRouter(s)
	go router.Start()

	s.Router = router
	s.Queue = queue

	err = s.loadTenantNumbers()
	if err != nil {
		return
	}
	err = s.loadTenants()
	if err != nil {
		return
	}

	err = s.loadEndpoints()
	if err != nil {
		return
	}

	// start freeswitch inbound event socket server
	fsSocket := NewEventSocketServer(s)
	fsSocket.Start()
	go func() {
		select {
		case err := <-fsSocket.Errors():
			logger.Logger.Fatal(err)
		}
	}()
	s.FsSocket = fsSocket

	// start web server
	// todo

	webIris := iris.New()

	s.loadWebPaths(webIris)
	webIris.Get("/health", func(ctx iris.Context) {
		ctx.StatusCode(200)
		return
	})

	err = webIris.Listen(gofaxlib.Config.Web.Listen)
	if err != nil {
		var lm = s.LogManager
		lm.SendLog(lm.BuildLog(
			"Server.Web",
			"GenericError",
			logrus.ErrorLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}

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
