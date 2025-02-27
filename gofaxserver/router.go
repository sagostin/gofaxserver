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
}

func (r *Router) routeFax(fax *FaxJob) {
	srcNum := r.server.DialplanManager.ApplyTransformationRules(fax.CallerIdNumber)
	dstNum := r.server.DialplanManager.ApplyTransformationRules(fax.CalleeNumber)

	fax.CalleeNumber = dstNum
	fax.CallerIdNumber = srcNum

	fax.Ts = time.Now() // time of start routing

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
