package gofaxserver

import (
	"github.com/sirupsen/logrus"
	"strconv"
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
	// todo
}

func (r *Router) routeFax(fax *FaxJob) {
	srcNum := r.server.DialplanManager.ApplyTransformationRules(fax.CallerIdNumber)
	dstNum := r.server.DialplanManager.ApplyTransformationRules(fax.CalleeNumber)

	fax.CalleeNumber = dstNum
	fax.CallerIdNumber = srcNum

	srcTenant, _ := r.server.getTenantByNumber(srcNum)
	if srcTenant != nil {
		fax.SrcTenantID = strconv.Itoa(int(srcTenant.ID))

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
		fax.DstTenantID = strconv.Itoa(int(dstTenant.ID))
	}

	r.server.Queue.QueueFaxResult <- QueueFaxResult{
		Job:      fax,
		Success:  fax.Result.Success,
		Response: fax.Result.ResultText,
	}

	fax.Ts = time.Now() // time of start routing

	switch srcType := fax.SourceRoutingInformation.SourceType; srcType {
	case "gateway":
		if contains(r.server.UpstreamFsGateways, fax.SourceRoutingInformation.Source) {
			// we need to route the message to the appropriate gateway / check if the destination is valid,
			// we could do pre-routing for this but, hey fuck it.

			// todo get endpoints for the fax based on destination number seen as it is from the carriers
			// once we have the endpoints for said number, then we can pass it to the "queue" runner
			// to start processing sending to the endpoints and such
			// if it fails on an endpoint, then we will go through the endpoints by priority
			// and if all endpoints fall, then we notify the destination tenant's notification email
			// configured on said number
			ep, err := r.server.getEndpointsForNumber(dstNum)
			if err != nil {
				r.server.LogManager.SendLog(r.server.LogManager.BuildLog(
					"Router",
					"failed to route from upstream to endpoint - caller: %s - callee: %s - err: %s",
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": fax.UUID.String()}, fax.CalleeNumber, fax.CallerIdName, err,
				))
				return // todo log error with no matching destination for fax from trunk
			}
			fax.Endpoints = ep

			r.server.Queue.Queue <- fax
			return
		}

		// do we actually have to check src sending number? can we not just check destination?
		// we could also validate that the sending / src address is coming from the right allowed endpoint?
		// do we want that much control where you need matching source / destination endpoints?

		// we'll just check for destination
		// this will only output if it's a valid tenant to tenant fax, otherwise, we will needa send to default gateway(s)
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

			/*			r.server.LogManager.SendLog(r.server.LogManager.BuildLog(
						"EventServer",
						"fax routing internal endpoint %v - %v - err: %v -- %v",
						logrus.InfoLevel,
						map[string]interface{}{"uuid": fax.UUID.String()}, fax.CalleeNumber, fax.CallerIdName, endpoints, err, n,
					))*/
		}
		fax.Endpoints = eps
		r.server.Queue.Queue <- fax
		return
	case "webhook":

		// we'll just check for destination
		// this will only output if it's a valid tenant to tenant fax, otherwise, we will needa send to default gateway(s)
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

			/*			r.server.LogManager.SendLog(r.server.LogManager.BuildLog(
						"EventServer",
						"fax routing internal endpoint %v - %v - err: %v -- %v",
						logrus.InfoLevel,
						map[string]interface{}{"uuid": fax.UUID.String()}, fax.CalleeNumber, fax.CallerIdName, endpoints, err, n,
					))*/
		}
		fax.Endpoints = eps
		r.server.Queue.Queue <- fax
	default:
		return // drop the fax and just return - we should delete the file...
		// todo
	}
	// todo
}
