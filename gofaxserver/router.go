package gofaxserver

import (
	"github.com/gonicus/gofaxip/gofaxlib"
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

type SourceRoutingInformation struct {
	Timestamp  time.Time
	SourceType string // the source of the message, could be a carrier, or a webhook, etc, or gateway
	Source     string // name of gateway, or webhook api key id or something
	SourceID   string // id of the source, could be the carrier id, or the webhook id, or uuid of channel id
}

func (r *Router) Start() {
	// we will have multiple channels to route messages to/from freeswitch, along with channels for the inbound webhook requests, etc.

	go func(server *Server) {
		for {
			// these are inbound messages from freeswitch (carrier trunks, vs pbx trunks)
			fax := <-server.faxJobRouting
			go r.routeFax(fax)
		}
		// todo
	}(r.server)

	// todo
}

func (r *Router) routeFax(fax *FaxJob) {
	srcNum := r.server.dialplanManager.ApplyTransformationRules(fax.CallerIdNumber)
	dstNum := r.server.dialplanManager.ApplyTransformationRules(fax.CalleeNumber)

	switch srcType := fax.SourceRoutingInformation.SourceType; srcType {
	case "gateway":
		if contains(gofaxlib.Config.FreeSwitch.Gateway, fax.SourceRoutingInformation.Source) {
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
				return // todo log error with no matching destination for fax from trunk
			}
			fax.Endpoints = ep
			return
		}

		_, err := r.server.getTenantByNumber(srcNum)
		// todo route to other tenant by dst number based on endpoint then send to queue

		if err != nil {
			return // todo throw error
		}

		// todo

		return
	case "webhook":
	// todo for webhooks, we will need to validate sending number to prevent abuse and such.
	default:
		return // drop the fax and just return - we should delete the file...
		// todo
	}
	// todo
}
