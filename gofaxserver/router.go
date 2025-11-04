package gofaxserver

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"strings"
	"time"
)

type Router struct {
	server   *Server
	numbers  []string // list of supported numbers, this will dynamically be reloaded when/if we add more numbers n garbage
	dialplan []string // list of supported dialplans, this will dynamically be reloaded when/if we add more dialplans n garbage
}

func NewRouter(server *Server) *Router {
	return &Router{server: server}
}

func (r *Router) Start() {
	// we will have multiple channels to route messages to/from freeswitch, along with channels for the inbound webhook requests, etc.
	for {
		// these are inbound messages from freeswitch (carrier trunks, vs pbx trunks)
		fax := <-r.server.FaxJobRouting
		go r.routeFax(fax)
	}
}

func (r *Router) routeFax(fax *FaxJob) {
	start := time.Now()

	// --- Pre-transform snapshot for logs
	origSrc := fax.CallerIdNumber
	origDst := fax.CalleeNumber

	// Apply dialplan transforms
	srcNum := r.server.DialplanManager.ApplyTransformationRules(fax.CallerIdNumber)
	dstNum := r.server.DialplanManager.ApplyTransformationRules(fax.CalleeNumber)
	fax.CalleeNumber = dstNum
	fax.CallerIdNumber = srcNum
	fax.Ts = time.Now()

	// Resolve tenants / caller ID meta
	var (
		srcTenantID uint
		dstTenantID uint
	)
	if srcTenant, _ := r.server.getTenantByNumber(srcNum); srcTenant != nil {
		fax.SrcTenantID = srcTenant.ID
		srcTenantID = srcTenant.ID

		if cid, _ := r.server.getNumber(srcNum); cid != nil {
			fax.CallerIdName = cid.Name
			fax.Header = cid.Header
		} else {
			fax.Header = fax.CallerIdName
		}
		fax.Identifier = fax.CallerIdNumber
	}
	if dstTenant, _ := r.server.getTenantByNumber(dstNum); dstTenant != nil {
		fax.DstTenantID = dstTenant.ID
		dstTenantID = dstTenant.ID
	}

	r.server.FaxTracker.Begin(fax)

	// Upstreams snapshot for logs
	upstreams := append([]string(nil), r.server.UpstreamFsGateways...)

	// High-level routing start log (INFO)
	r.server.LogManager.SendLog(
		r.server.LogManager.BuildLog(
			"Router",
			"fax routing start",
			logrus.InfoLevel,
			map[string]interface{}{
				"uuid":            fax.UUID.String(),
				"elapsed_ms":      0,
				"orig_src":        origSrc,
				"orig_dst":        origDst,
				"src_transformed": srcNum,
				"dst_transformed": dstNum,
				"src_tenant_id":   srcTenantID,
				"dst_tenant_id":   dstTenantID,
				"source_type":     fax.SourceInfo.SourceType,
				"source_id":       fax.SourceInfo.Source,
				"upstreams_count": len(upstreams),
				"upstreams":       upstreams,
				"caller_id_name":  fax.CallerIdName,
				"header":          fax.Header,
			},
		),
	)

	// Emit a lightweight progress tick to UI/consumers
	r.server.Queue.QueueFaxResult <- QueueFaxResult{Job: fax}

	// --- Source-type specific short-circuit (e.g., came from a trusted upstream gateway)
	switch fax.SourceInfo.SourceType {
	case "gateway":
		if contains(r.server.UpstreamFsGateways, fax.SourceInfo.Source) {
			endpoints, err := r.server.getEndpointsForNumber(dstNum)
			if err != nil {
				r.server.LogManager.SendLog(
					r.server.LogManager.BuildLog(
						"Router",
						"upstream->endpoint lookup failed",
						logrus.ErrorLevel,
						map[string]interface{}{
							"uuid":     fax.UUID.String(),
							"callee":   dstNum,
							"caller":   srcNum,
							"error":    err.Error(),
							"upstream": fax.SourceInfo.Source,
						},
					),
				)
				// Fall through to default routing below
			} else {
				// Success path: log endpoints then queue
				r.server.LogManager.SendLog(
					r.server.LogManager.BuildLog(
						"Router",
						"upstream->endpoint routing",
						logrus.InfoLevel,
						map[string]interface{}{
							"uuid":            fax.UUID.String(),
							"callee":          dstNum,
							"caller":          srcNum,
							"upstream":        fax.SourceInfo.Source,
							"endpoints_count": len(endpoints),
							"endpoints":       summarizeEndpoints(endpoints),
						},
					),
				)
				fax.Endpoints = endpoints
				r.server.Queue.Queue <- fax
				return
			}
		}
	}

	// --- Standard: try direct endpoints for the destination
	endpoints, err := r.server.getEndpointsForNumber(dstNum)
	if err == nil && len(endpoints) > 0 {
		r.server.LogManager.SendLog(
			r.server.LogManager.BuildLog(
				"Router",
				"direct endpoint routing",
				logrus.InfoLevel,
				map[string]interface{}{
					"uuid":            fax.UUID.String(),
					"callee":          dstNum,
					"caller":          srcNum,
					"endpoints_count": len(endpoints),
					"endpoints":       summarizeEndpoints(endpoints),
				},
			),
		)
		fax.Endpoints = endpoints
		r.server.Queue.Queue <- fax
		return
	}

	// --- Fallback: route to upstream FreeSWITCH gateways (priority 999 global batch)
	if len(upstreams) == 0 {
		// Nothing to fall back to—log and drop (or you could NACK/notify)
		r.server.LogManager.SendLog(
			r.server.LogManager.BuildLog(
				"Router",
				"no direct endpoints and no upstreams available",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":   fax.UUID.String(),
					"callee": dstNum,
					"caller": srcNum,
					"error":  fmt.Sprintf("getEndpointsForNumber failed or returned none: %v", err),
				},
			),
		)
		return
	}

	r.server.LogManager.SendLog(
		r.server.LogManager.BuildLog(
			"Router",
			"falling back to upstream gateways",
			logrus.WarnLevel,
			map[string]interface{}{
				"uuid":            fax.UUID.String(),
				"callee":          dstNum,
				"caller":          srcNum,
				"reason_error":    fmt.Sprintf("%v", err),
				"upstreams_count": len(upstreams),
				"upstreams":       upstreams,
			},
		),
	)

	eps := make([]*Endpoint, 0, len(upstreams))
	for _, up := range upstreams {
		eps = append(eps, &Endpoint{
			EndpointType: "gateway",
			Endpoint:     up,
			Priority:     999,
			Type:         "global",
			TypeID:       0,
		})
	}

	r.server.LogManager.SendLog(
		r.server.LogManager.BuildLog(
			"Router",
			"enqueued to upstreams",
			logrus.InfoLevel,
			map[string]interface{}{
				"uuid":            fax.UUID.String(),
				"callee":          dstNum,
				"caller":          srcNum,
				"endpoints_count": len(eps),
				"endpoints":       summarizeEndpoints(eps),
				"elapsed_ms":      time.Since(start).Milliseconds(),
			},
		),
	)

	// Send to the queue
	fax.Endpoints = eps
	// in routeFax, right before you queue:
	r.server.Queue.Queue <- fax
}

// summarizeEndpoints produces a compact, log-friendly view of endpoints.
func summarizeEndpoints(eps []*Endpoint) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(eps))
	for _, e := range eps {
		out = append(out, map[string]interface{}{
			"type":          e.EndpointType, // e.g., "gateway", "webhook"
			"endpoint":      e.Endpoint,     // address/URL/gateway name
			"priority":      e.Priority,     // uint
			"strategy_type": e.Type,         // e.g., "global"
			"type_id":       e.TypeID,
		})
	}
	return out
}

// Updated detectAndRouteToBridge: first attempts to find a bridge-enabled endpoint
// for the destination number; if none found, attempts the same for the source number.
func (r *Router) detectAndRouteToBridge(dstNumber string, srcNumber string, srcGateway string) (string, bool) {
	// steps to detect and route to bridge
	// 1. if source gateway is in the upstream list, check the destination number, and then see if the corresponding
	// gateway for destination number has bridging enabled
	// 2. if outbound, from not an upstream gateway, check the source gateway to see if bridging is enabled
	// if it is, bridge to far end using upstream gateways

	// Helper function to check a given number’s endpoints for a single bridge-enabled gateway

	// Only attempt bridging if the call came in via an upstream Freeswitch gateway
	if contains(r.server.UpstreamFsGateways, srcGateway) {
		//logrus.Printf("upstreams: %v", r.server.UpstreamFsGateways)
		// if the upstream gateways contains the src gateway, then we want to check if the destination number has an endpoint
		// if the destination has an endpoint, and it has bridge mode enabled, then continue

		bridgeGw, b, err := r.server.checkForBridge(dstNumber)
		if err != nil {
			// this will just continue with normal routing if that's the case
			return "", false
		}
		return bridgeGw, b
	}
	// if the src gateway is not an upstream
	// check the source number to see if it is to be bridged for outbound, also check if the destination is also on the fax server
	// if the destination is on the fax server, we will follow what the far end has enabled, so it will override the bridge "mode"

	ep, err := r.server.getEndpointByName(srcGateway)
	if err != nil || ep == nil {
		return "", false
	}

	if !ep.Bridge {
		return "", false
	}

	// if it is in bridge mode, then also double check to see if the destination number is on the gateway
	bridgeDst, bDst, err := r.server.checkForBridge(dstNumber)
	if bDst {
		return bridgeDst, bDst
	}

	if ep.Bridge {
		// todo make this support all the upstreams
		return "upstream", true
	}
	// todo print error?
	return "", false
}

func (s *Server) checkForBridge(numberToCheck string) (string, bool, error) {
	epList, err := s.getEndpointsForNumber(numberToCheck)
	//logrus.Printf("epList: %v", epList)
	if err != nil {
		return "", false, err
	}
	for _, endpoint := range epList {
		//logrus.Printf("endpoint: %v", endpoint)
		if endpoint.Bridge && endpoint.EndpointType == "gateway" {
			epParts := strings.Split(endpoint.Endpoint, ":")
			//logrus.Printf("endpoint 2: %v", epParts[0])
			return epParts[0], true, nil
		}
	}
	return "", false, errors.New("unable to find matching endpoint for number")
}
