package gofaxserver

import "fmt"

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

	fmt.Println(fax)

	switch srcType := fax.SourceRoutingInformation.SourceType; srcType {
	case "gateway":
		/*if contains(r.server.UpstreamFsGateways, fax.SourceRoutingInformation.Source) {
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
				fmt.Print(err)
				return // todo log error with no matching destination for fax from trunk
			}
			fax.Endpoints = ep

			r.server.Queue.Queue <- fax
			return
		}
		*/
		// do we actually have to check src sending number? can we not just check destination?
		// we could also validate that the sending / src address is coming from the right allowed endpoint?
		// do we want that much control where you need matching source / destination endpoints?

		_, err := r.server.getTenantByNumber(srcNum)
		// todo route to other tenant by dst number based on endpoint then send to queue
		if err != nil {
			// todo throw error cuz not valid sending number
		}

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
		}
		fax.Endpoints = eps
		r.server.Queue.Queue <- fax
		return
	case "webhook":
	// todo for webhooks, we will need to validate sending number to prevent abuse and such.
	default:
		return // drop the fax and just return - we should delete the file...
		// todo
	}
	// todo
}
