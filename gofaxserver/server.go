package gofaxserver

import (
	"fmt"
	"github.com/kataras/iris/v12"
	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Server struct {
	FsSocket      *EventSocketServer   `json:"fs_socket,omitempty"`
	Router        *Router              `json:"router,omitempty"`
	Queue         *Queue               `json:"queue,omitempty"`
	LogManager    *gofaxlib.LogManager `json:"log_manager,omitempty"`
	FaxJobRouting chan *FaxJob         `json:"fax_job_routing,omitempty"`
	DB            *gorm.DB             `json:"d_b,omitempty"`
	// dialplan holds the active DialplanManager; swapped atomically on reload
	// in "db" source mode. Access via Dialplan().
	dialplan atomic.Pointer[DialplanManager]
	// In-memory maps for Tenants and TenantNumbers.
	mu            sync.RWMutex
	Tenants       map[uint]*Tenant         `json:"tenants,omitempty"`        // keyed by Tenant.ID
	TenantNumbers map[string]*TenantNumber `json:"tenant_numbers,omitempty"` // keyed by the phone number string
	// Endpoints assigned directly to a tenant (keyed by tenant ID).
	TenantEndpoints map[uint][]*Endpoint `json:"tenant_endpoints,omitempty"`
	// Endpoints assigned to a specific number (keyed by the phone number string).
	NumberEndpoints     map[string][]*Endpoint `json:"number_endpoints,omitempty"`
	Endpoints           map[string]*Endpoint
	GatewayEndpointsACL []string    `json:"gateway_endpoint_acl,omitempty"` // allowed source IPs for SIP trunks
	UpstreamFsGateways  []string    `json:"upstream_fs_gateways"`           // upstream gateway names defined in the global endpoints config/DB
	FaxTracker          *FaxTracker `json:"fax_tracker,omitempty"`
	// faxPolicies holds the active fax policy rules; swapped atomically on
	// reload. Access via ResolveFaxPolicy().
	faxPolicies atomic.Pointer[[]FaxPolicyRule]
	// pairStates is the write-through cache of persisted flip-flop pair
	// state (fax_pair_states table), guarded by pairStateMu.
	pairStateMu sync.Mutex
	pairStates  map[string]*FaxPairState
}

// Dialplan returns the active DialplanManager (hot-reload safe). Falls back
// to the built-in defaults if none has been stored yet, so callers never
// dereference a nil manager.
func (s *Server) Dialplan() *DialplanManager {
	if dm := s.dialplan.Load(); dm != nil {
		return dm
	}
	return NewDialplanManager(DefaultTransformationRules())
}

func NewServer() *Server {
	return &Server{FaxJobRouting: make(chan *FaxJob),
		Tenants:         make(map[uint]*Tenant),
		TenantNumbers:   make(map[string]*TenantNumber),
		TenantEndpoints: make(map[uint][]*Endpoint),
		NumberEndpoints: make(map[string][]*Endpoint),
		pairStates:      make(map[string]*FaxPairState),
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

	s.FaxTracker = NewFaxTracker()

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Server.StartUp",
		fmt.Sprintf("starting gofaxserver"),
		logrus.InfoLevel,
		nil,
	))

	// In "config" mode the dialplan is static and can be loaded immediately.
	// In "db" mode it is loaded after the database connection below.
	if dialplanSource() == "config" {
		s.dialplan.Store(loadDialplan())
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			fmt.Sprintf("loaded dialplan and transformations (source=config)"),
			logrus.InfoLevel,
			nil,
		))
	}

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

	if err := s.seedGatewayTemplates(); err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			fmt.Sprintf("failed to seed gateway templates: %v", err),
			logrus.ErrorLevel,
			nil,
		))
	}

	// Load the Postgres-backed fax policy rules (T.38/ECM/V.17) and the
	// persisted flip-flop pair state. These replace the old FreeSWITCH
	// mod_db softmodem fallback and the in-memory pair map.
	if err := s.reloadFaxPolicies(); err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			fmt.Sprintf("failed to load fax policies: %v", err),
			logrus.ErrorLevel,
			nil,
		))
	}
	s.loadPairStates()

	if dialplanSource() == "db" {
		dm, err := s.loadDialplanFromDB()
		if err != nil {
			// Never store a nil manager: every routed call dereferences it.
			// Fall back to the built-in defaults until a successful reload.
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"Server.StartUp",
				fmt.Sprintf("failed to load dialplan from database (using built-in defaults until reload): %v", err),
				logrus.ErrorLevel,
				nil,
			))
			dm = NewDialplanManager(DefaultTransformationRules())
		}
		s.dialplan.Store(dm)
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Server.StartUp",
			fmt.Sprintf("loaded dialplan and transformations (source=db)"),
			logrus.InfoLevel,
			nil,
		))
	}

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

	// start the gateway registration monitor (no-op unless gateways with
	// register=true are provisioned; disabled when gateway_monitor_seconds < 0)
	go s.startGatewayMonitor()

	// start the temp-file janitor (removes orphaned fax files from temp_dir;
	// see tempclean.go)
	go s.startTempJanitor()

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
		"started web server on %s",
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

// loadDialplan builds the DialplanManager from configuration.
//
// If the `dialplan` section is absent from config.json, the built-in default
// rules are used (see DefaultTransformationRules). If the section is present,
// its rules fully replace the defaults — an empty rules list disables
// transformation entirely. An invalid pattern falls back to the defaults so
// a typo can never silently break routing.
func loadDialplan() *DialplanManager {
	cfg := gofaxlib.Config.Dialplan
	if cfg == nil {
		return NewDialplanManager(DefaultTransformationRules())
	}

	rules := make([]TransformationRule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rule, err := compileRule(r.Pattern, r.Replacement)
		if err != nil {
			log.Printf("Dialplan: invalid pattern %q (%v); falling back to default rules", r.Pattern, err)
			return NewDialplanManager(DefaultTransformationRules())
		}
		rules = append(rules, rule)
	}
	return NewDialplanManager(rules)
}

// ReloadData reloads endpoints, tenants, and tenant users from the database,
// updating the in-memory maps. In "db" dialplan mode it also hot-reloads the
// transformation rules.
func (s *Server) ReloadData() error {
	if err := s.loadEndpoints(); err != nil {
		return fmt.Errorf("failed to reload endpoints: %w", err)
	}
	if err := s.reloadTenantsAndNumbers(); err != nil {
		return fmt.Errorf("failed to reload tenants: %w", err)
	}
	if err := s.reloadDialplan(); err != nil {
		return fmt.Errorf("failed to reload dialplan: %w", err)
	}
	if err := s.reloadFaxPolicies(); err != nil {
		return fmt.Errorf("failed to reload fax policies: %w", err)
	}
	return nil
}
