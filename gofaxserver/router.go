package gofaxserver

import (
	"errors"
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
	srcNum := r.server.DialplanManager.ApplyTransformationRules(fax.CallerIdNumber)
	dstNum := r.server.DialplanManager.ApplyTransformationRules(fax.CalleeNumber)

	fax.CalleeNumber = dstNum
	fax.CallerIdNumber = srcNum

	fax.Ts = time.Now() // time of start routing

	srcTenant, _ := r.server.getTenantByNumber(srcNum)
	if srcTenant != nil {
		fax.SrcTenantID = srcTenant.ID
		cid, _ := r.server.getNumber(srcNum)
		if cid != nil {
			fax.CallerIdName = cid.Name
			fax.Header = cid.Header
		} else {
			fax.Header = fax.CallerIdName
		}
		fax.Identifier = fax.CallerIdNumber
	}
	dstTenant, _ := r.server.getTenantByNumber(dstNum)
	if dstTenant != nil {
		fax.DstTenantID = dstTenant.ID
	}

	r.server.Queue.QueueFaxResult <- QueueFaxResult{
		Job: fax,
	}

	// source type specific routing
	switch srcType := fax.SourceInfo.SourceType; srcType {
	case "gateway":
		if contains(r.server.UpstreamFsGateways, fax.SourceInfo.Source) {
			ep, err := r.server.getEndpointsForNumber(dstNum)
			if err != nil {
				r.server.LogManager.SendLog(r.server.LogManager.BuildLog(
					"Router",
					"failed to route from upstream to endpoint - caller: %s - callee: %s - err: %s",
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": fax.UUID.String()}, fax.CalleeNumber, fax.CallerIdName, err,
				))
				return
			}
			fax.Endpoints = ep
			r.server.Queue.Queue <- fax
			return
		}
	}

	// otherwise continue to the rest of the routing steps
	endpoints, err := r.server.getEndpointsForNumber(dstNum)
	if err == nil {
		fax.Endpoints = endpoints
		r.server.Queue.Queue <- fax
		return
	}

	// route to default gateway for freeswitch
	var eps []*Endpoint
	for _, ep := range r.server.UpstreamFsGateways {
		eps = append(endpoints, &Endpoint{EndpointType: "gateway", Endpoint: ep, Priority: 999})
	}

	// send to the queue
	fax.Endpoints = eps
	r.server.Queue.Queue <- fax
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
		// if the upstream gateways contains the src gateway, then we want to check if the destination number has an endpoint
		// if the destination has an endpoint, and it has bridge mode enabled, then continue

		bridge, b, err := r.server.checkForBridge(dstNumber)
		if err != nil {
			// this will just continue with normal routing if that's the case
			return "", false
		}
		return bridge, b
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
	if err != nil {
		return "", false, err
	}
	for _, endpoint := range epList {
		if endpoint.Bridge && len(epList) == 1 && endpoint.Type == "gateway" {
			epParts := strings.Split(endpoint.Endpoint, ":")
			return epParts[0], true, nil
		}
	}
	return "", false, errors.New("unable to find matching endpoint for number")
}
