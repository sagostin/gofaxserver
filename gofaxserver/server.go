package gofaxserver

import (
	"fmt"
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
	if gofaxlib.Config.Loki.Job == "" {
		gofaxlib.Config.Loki.Job = "faxserver"
	}

	/*err := godotenv.Load()
	if err != nil {
		return
	}*/

	logManager := gofaxlib.NewLogManager(gofaxlib.NewLokiClient())
	logManager.LoadTemplates()
	s.LogManager = logManager

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server.StartUp",
		fmt.Sprintf("starting gofaxserver"),
		logrus.InfoLevel,
		nil,
	))

	s.DialplanManager = loadDialplan()
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server.StartUp",
		fmt.Sprintf("loaded dialplan and transformations"),
		logrus.InfoLevel,
		nil,
	))

	// Shut down receiving lines when killed
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	db, err := gorm.Open(postgres.Open(getPostgresDSN()), &gorm.Config{})
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			"failed to connect to database: %v",
			logrus.ErrorLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}
	s.DB = db

	err = s.migrateSchema()
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			"failed to connect to migrate schema: %v",
			logrus.ErrorLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server.StartUp",
		fmt.Sprintf("connected to database"),
		logrus.InfoLevel,
		nil,
	))

	err = s.loadTenantNumbers()
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			"failed to connect to load numbers: %v",
			logrus.FatalLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}
	err = s.loadTenants()
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			"failed to connect to load tenants: %v",
			logrus.FatalLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}

	err = s.loadEndpoints()
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			"failed to connect to load endpoints: %v",
			logrus.FatalLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server.StartUp",
		fmt.Sprintf("loaded tenants, numbers, and endpoints"),
		logrus.InfoLevel,
		nil,
	))

	// start the router
	queue := NewQueue(s)
	go queue.Start()

	router := NewRouter(s)
	go router.Start()

	s.Router = router
	s.Queue = queue

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server",
		fmt.Sprintf("started queue and router"),
		logrus.InfoLevel,
		nil,
	))

	// start freeswitch inbound event socket server
	fsSocket := NewEventSocketServer(s)
	fsSocket.Start()
	go func() {
		select {
		case err := <-fsSocket.Errors():
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"Server.StartUp",
				"error received from fs socket: %v",
				logrus.ErrorLevel,
				nil,
				err,
			))
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

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server",
		fmt.Sprintf("started web server on %s"),
		logrus.InfoLevel,
		nil, gofaxlib.Config.Web.Listen,
	))

	err = webIris.Listen(gofaxlib.Config.Web.Listen)
	if err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.Web",
			"GenericError",
			logrus.FatalLevel,
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

// ReloadData reloads endpoints, tenants, and tenant users from the database,
// updating the in-memory maps.
func (s *Server) ReloadData() error {
	if err := s.loadEndpoints(); err != nil {
		return fmt.Errorf("failed to reload endpoints: %w", err)
	}
	if err := s.loadTenants(); err != nil {
		return fmt.Errorf("failed to reload tenants: %w", err)
	}
	return nil
}
