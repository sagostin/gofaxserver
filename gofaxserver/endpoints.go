// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

package gofaxserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// aclResolver resolves hostname-valued gateway ACL entries to IPs.
// Package-level var so tests can stub DNS.
var aclResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
} = net.DefaultResolver

// aclResolveTimeout bounds a single DNS lookup during ACL resolution.
const aclResolveTimeout = 5 * time.Second

// splitGatewayACLHost returns the host portion of a "name:host" gateway ACL
// entry. Gateway names never contain ':' (see gatewayNameRe), so splitting
// on the first colon is unambiguous.
func splitGatewayACLHost(entry string) (host string, ok bool) {
	idx := strings.Index(entry, ":")
	if idx < 0 || idx+1 >= len(entry) {
		return "", false
	}
	return entry[idx+1:], true
}

// resolveGatewayACLs resolves hostname-valued gateway ACL entries to their
// IP addresses. Literal-IP and legacy IP-only entries are skipped (they match
// directly in fsGatewayACL). Returns the resolved map plus the entries whose
// lookup failed. Performs DNS lookups — call without holding s.mu.
func resolveGatewayACLs(entries []string) (resolved map[string][]string, failures map[string]error) {
	resolved = make(map[string][]string)
	failures = make(map[string]error)
	for _, entry := range entries {
		host, ok := splitGatewayACLHost(entry)
		if !ok || net.ParseIP(host) != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), aclResolveTimeout)
		addrs, err := aclResolver.LookupIPAddr(ctx, host)
		cancel()
		if err != nil {
			failures[entry] = err
			continue
		}
		ips := make([]string, 0, len(addrs))
		for _, a := range addrs {
			ips = append(ips, a.IP.String())
		}
		resolved[entry] = ips
	}
	return resolved, failures
}

// this will control the endpoints, endpoints are the gateways/sip trunks,
// or webhooks that will be used to deliver faxes and such

/*

- we will support adding endpoints to tenants dynamically via the api and the web interface (eventually)
- will need to figure out how to be able to add new xml to freeswitch to configure new FS gateways / endpoints

*/

// endpoints are assigned to either a tenant or a tenant's number, so we can have multiple numbers with different endpoints
// when an endpoint has a tenant endpoint and a tenant endpoint, the number endpoint will take priority

type Endpoint struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Type         string `json:"type"`          // tenant, or number, or "global"
	TypeID       uint   `json:"type_id"`       // id of type, either tenant or number id
	EndpointType string `json:"endpoint_type"` // webhook, gateway (gateway is for freeswitch), or email, or freeswitch (which would be in the format of say /external/sofia/gateway/gatewayname or something else for TDM routing)
	Endpoint     string `json:"endpoint"`      // if type=gateway then this is the freeswitch gateway name, otherwise, it's the webhook url, or email address (or multiple email addresses separated by semi-colons), gateways will have gatewayname:publicIP for acl rules / matching
	Priority     uint   `json:"priority"`      // priority from 0 to any? 0 being the highest priority - if priority is 666 then we will ignore it as an option for sending out? or should we handle based on general var in
	Bridge       bool   `json:"bridge"`        // if gateway for fax is set to bridge / from a bridge enabled gateway, it will use bridge mode instead of rxfax and txfax
}

// todo

// loadEndpoints loads all Endpoint records from the database into the in-memory maps.
func (s *Server) loadEndpoints() error {
	var endpoints []Endpoint
	if err := s.DB.Find(&endpoints).Error; err != nil {
		return fmt.Errorf("failed to load endpoints: %w", err)
	}

	s.mu.Lock()

	// Reset the endpoint maps.
	s.TenantEndpoints = make(map[uint][]*Endpoint)
	s.NumberEndpoints = make(map[string][]*Endpoint)
	s.Endpoints = make(map[string]*Endpoint)
	s.UpstreamFsGateways = make([]string, 0)
	s.GatewayEndpointsACL = []string{}

	// Process each endpoint and place it in the proper map.
	for _, ep := range endpoints {
		// add to general endpoint map with TYPE/ENDPOINT
		s.Endpoints[ep.Type+"/"+ep.Endpoint] = &ep

		epCopy := ep // create a copy for taking a pointer
		switch epCopy.EndpointType {
		case "gateway":
			// add any endpoints with type of gateway to the ACL list for allowed FS calls
			s.GatewayEndpointsACL = append(s.GatewayEndpointsACL, ep.Endpoint)
		}

		switch epCopy.Type {
		case "global":
			switch epCopy.EndpointType {
			case "gateway":
				s.UpstreamFsGateways = append(s.UpstreamFsGateways, strings.Split(ep.Endpoint, ":")[0])
			}
		case "tenant":
			s.TenantEndpoints[epCopy.TypeID] = append(s.TenantEndpoints[epCopy.TypeID], &epCopy)
		case "number":
			// Find the phone number corresponding to this tenant number ID.
			var numberStr string
			for _, tn := range s.TenantNumbers {
				if tn.ID == epCopy.TypeID {
					numberStr = tn.Number
					break
				}
			}
			if numberStr == "" {
				// Optionally log a warning if a tenant number isn’t found.
				s.LogManager.SendLog(s.LogManager.BuildLog(
					"Endpoint.Load",
					fmt.Sprintf("tenant number with id %d not found for endpoint id %d", epCopy.TypeID, epCopy.ID),
					logrus.WarnLevel,
					nil,
				))
				continue
			}
			s.NumberEndpoints[numberStr] = append(s.NumberEndpoints[numberStr], &epCopy)
		default:
			// Optionally handle other endpoint types (e.g. "global") if needed.
		}
	}
	aclEntries := append([]string(nil), s.GatewayEndpointsACL...)
	s.mu.Unlock()

	// Resolve hostname-valued ACL entries outside the lock — DNS lookups may
	// block and must not stall inbound call handling. Entries that fail to
	// resolve here fail closed (no IPs) until the gateway monitor re-resolves.
	resolved, failures := resolveGatewayACLs(aclEntries)
	for entry, err := range failures {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Endpoint.Load",
			fmt.Sprintf("gateway ACL entry %q failed DNS resolution: %v (inbound calls will fail ACL until resolved)", entry, err),
			logrus.WarnLevel,
			map[string]interface{}{"endpoint": entry},
		))
	}
	s.mu.Lock()
	s.GatewayACLResolved = resolved
	s.mu.Unlock()
	return nil
}

// addEndpointToDB persists a new endpoint to the database and then adds it to the in-memory map.
func (s *Server) addEndpointToDB(endpoint *Endpoint) error {
	// Persist the endpoint to the database.
	if err := s.DB.Create(endpoint).Error; err != nil {
		return fmt.Errorf("failed to add endpoint to database: %w", err)
	}

	// Add the endpoint to the in-memory map.
	s.mu.Lock()
	defer s.mu.Unlock()
	switch endpoint.Type {
	case "tenant":
		s.TenantEndpoints[endpoint.TypeID] = append(s.TenantEndpoints[endpoint.TypeID], endpoint)
	case "number":
		// Find the tenant number's phone string.
		var numberStr string
		for _, tn := range s.TenantNumbers {
			if tn.ID == endpoint.TypeID {
				numberStr = tn.Number
				break
			}
		}
		if numberStr == "" {
			return fmt.Errorf("tenant number with id %d not found", endpoint.TypeID)
		}
		s.NumberEndpoints[numberStr] = append(s.NumberEndpoints[numberStr], endpoint)
	case "global":
		// Global endpoints represent upstream carrier gateways shared across all tenants.
		// type_id is unused (0); only the gateway name (Endpoint) matters for routing.
		if endpoint.EndpointType == "gateway" {
			gwName := strings.Split(endpoint.Endpoint, ":")[0]
			s.UpstreamFsGateways = append(s.UpstreamFsGateways, gwName)
		}
		s.Endpoints[endpoint.Type+"/"+endpoint.Endpoint] = endpoint
	default:
		return fmt.Errorf("unknown endpoint type: %s", endpoint.Type)
	}
	return nil
}

// removeEndpointFromDB deletes an endpoint from the database and removes it from the in-memory maps.
func (s *Server) removeEndpointFromDB(endpointID uint) error {
	// Delete the endpoint from the database.
	if err := s.DB.Delete(&Endpoint{}, endpointID).Error; err != nil {
		return fmt.Errorf("failed to delete endpoint from database: %w", err)
	}
	// Remove it from the in-memory maps.
	return s.removeEndpoint(endpointID)
}

// updateEndpoint updates an existing endpoint in the database and then refreshes it in the in-memory maps.
func (s *Server) updateEndpoint(endpoint *Endpoint) error {
	// Save the updated endpoint to the database.
	if err := s.DB.Save(endpoint).Error; err != nil {
		return fmt.Errorf("failed to update endpoint in database: %w", err)
	}

	// Remove the existing endpoint from the in-memory maps.
	if err := s.removeEndpoint(endpoint.ID); err != nil {
		return fmt.Errorf("failed to remove old endpoint from memory: %w", err)
	}

	// Add the updated endpoint back into the in-memory maps.
	s.mu.Lock()
	defer s.mu.Unlock()
	switch endpoint.Type {
	case "tenant":
		s.TenantEndpoints[endpoint.TypeID] = append(s.TenantEndpoints[endpoint.TypeID], endpoint)
	case "number":
		var numberStr string
		for _, tn := range s.TenantNumbers {
			if tn.ID == endpoint.TypeID {
				numberStr = tn.Number
				break
			}
		}
		if numberStr == "" {
			return fmt.Errorf("tenant number with id %d not found", endpoint.TypeID)
		}
		s.NumberEndpoints[numberStr] = append(s.NumberEndpoints[numberStr], endpoint)
	case "global":
		if endpoint.EndpointType == "gateway" {
			gwName := strings.Split(endpoint.Endpoint, ":")[0]
			s.UpstreamFsGateways = append(s.UpstreamFsGateways, gwName)
		}
		s.Endpoints[endpoint.Type+"/"+endpoint.Endpoint] = endpoint
	default:
		return fmt.Errorf("unknown endpoint type: %s", endpoint.Type)
	}
	return nil
}

// addEndpoint adds a new endpoint. Depending on endpoint.Type, it is stored under the tenant or the number.
func (s *Server) addEndpoint(endpoint *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch endpoint.Type {
	case "tenant":
		// For tenant endpoints, TypeID is the tenant's ID.
		s.TenantEndpoints[endpoint.TypeID] = append(s.TenantEndpoints[endpoint.TypeID], endpoint)
	case "number":
		// For number endpoints, we must first find the tenant number using the TenantNumber ID.
		var numberStr string
		for _, tn := range s.TenantNumbers {
			if tn.ID == endpoint.TypeID {
				numberStr = tn.Number
				break
			}
		}
		if numberStr == "" {
			return fmt.Errorf("tenant number with id %d not found", endpoint.TypeID)
		}
		s.NumberEndpoints[numberStr] = append(s.NumberEndpoints[numberStr], endpoint)
	case "global":
		if endpoint.EndpointType == "gateway" {
			gwName := strings.Split(endpoint.Endpoint, ":")[0]
			s.UpstreamFsGateways = append(s.UpstreamFsGateways, gwName)
		}
		s.Endpoints[endpoint.Type+"/"+endpoint.Endpoint] = endpoint
	default:
		return fmt.Errorf("unknown endpoint type: %s", endpoint.Type)
	}
	return nil
}

// removeEndpoint removes an endpoint by its ID. This function scans both maps.
func (s *Server) removeEndpoint(endpointID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from tenant endpoints.
	for tenantID, endpoints := range s.TenantEndpoints {
		for i, ep := range endpoints {
			if ep.ID == endpointID {
				s.TenantEndpoints[tenantID] = append(endpoints[:i], endpoints[i+1:]...)
				return nil
			}
		}
	}

	// Remove from number endpoints.
	for numberStr, endpoints := range s.NumberEndpoints {
		for i, ep := range endpoints {
			if ep.ID == endpointID {
				s.NumberEndpoints[numberStr] = append(endpoints[:i], endpoints[i+1:]...)
				return nil
			}
		}
	}

	// Remove from global endpoints (UpstreamFsGateways + Endpoints map).
	for i, gwName := range s.UpstreamFsGateways {
		// Find the matching endpoint by its gateway-name prefix.
		matched := false
		for _, ep := range s.Endpoints {
			if ep.ID == endpointID && ep.Type == "global" && strings.HasPrefix(ep.Endpoint, gwName+":") || (ep.ID == endpointID && ep.Type == "global" && ep.Endpoint == gwName) {
				matched = true
				break
			}
		}
		if matched {
			s.UpstreamFsGateways = append(s.UpstreamFsGateways[:i], s.UpstreamFsGateways[i+1:]...)
			break
		}
	}
	for k, ep := range s.Endpoints {
		if ep.ID == endpointID {
			delete(s.Endpoints, k)
			return nil
		}
	}

	return fmt.Errorf("endpoint with id %d not found", endpointID)
}

func (s *Server) fsGatewayACL(ip string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.GatewayEndpointsACL {
		// Gateway endpoints are stored as "name:publicIP". Compare the IP
		// portion exactly — substring matching would let e.g. source IP
		// "92.168.1.1" match an ACL entry for "192.168.1.10".
		if idx := strings.Index(k, ":"); idx >= 0 {
			if k[idx+1:] == ip {
				return k, nil
			}
			continue
		}
		// Legacy entries without an IP suffix match only on exact equality
		// (e.g. an entry that is just an IP address).
		if k == ip {
			return k, nil
		}
	}

	// Hostname-valued entries match against their last successful DNS
	// resolution (see resolveGatewayACLs / gateway monitor refresh).
	for _, k := range s.GatewayEndpointsACL {
		for _, resolvedIP := range s.GatewayACLResolved[k] {
			if resolvedIP == ip {
				return k, nil
			}
		}
	}

	return "", errors.New("unable to find matching gateway from sending IP")
}

// getEndpointByName gets the endpoint by given endpoint name - eg. pbx_test
func (s *Server) getEndpointByName(name string) (*Endpoint, error) {
	for epName, ep := range s.Endpoints {
		if strings.Contains(epName, name) {
			return ep, nil
		}
		continue
	}

	return nil, errors.New("unable to find endpoint by name")
}

func endpointGatewayDialstring(endpoints []string, dstNum string) string {
	var dsGateways string

	for n, gw := range endpoints {
		if n > 0 {
			dsGateways += "," // prepend comma before all but the first
		}
		dsGateways += fmt.Sprintf("sofia/gateway/%v/%v", gw, dstNum)
	}

	return dsGateways
}

// bridgeGatewayTagVar is the per-leg channel variable that records which
// upstream gateway an upstream-direction bridge leg was dialed through. Each
// b-leg exports it to the a-leg on answer (export_vars), so the winning
// gateway shows up as variable_gofax_bridge_gw on the a-leg's events.
const bridgeGatewayTagVar = "gofax_bridge_gw"

// outboundGatewayTagVar is the per-leg channel variable that records which
// gateway an outbound (txfax) call leg was dialed through. Unlike the bridge
// case there is no b-leg: the originated channel IS the gateway leg, so the
// winning leg's variable rides the channel directly and shows up as
// variable_gofax_gw on its events (spandsp custom events carry channel
// variables).
const outboundGatewayTagVar = "gofax_gw"

// endpointGatewayDialstringTagged builds the same comma-separated failover
// dialstring as endpointGatewayDialstring, but tags every leg with
// bridgeGatewayTagVar=<gw> and export_vars so the a-leg learns which gateway
// actually answered once bridged. Gateway attribution only — the failover
// order and call flow are unchanged.
func endpointGatewayDialstringTagged(endpoints []string, dstNum string) string {
	var dsGateways string

	for n, gw := range endpoints {
		if n > 0 {
			dsGateways += ","
		}
		dsGateways += fmt.Sprintf("[%s=%v,export_vars=%s]sofia/gateway/%v/%v",
			bridgeGatewayTagVar, gw, bridgeGatewayTagVar, gw, dstNum)
	}

	return dsGateways
}

// endpointGatewayDialstringOutboundTagged builds the same comma-separated
// failover dialstring as endpointGatewayDialstring, but tags every leg with
// outboundGatewayTagVar=<gw> so the winning channel (the txfax leg itself)
// carries variable_gofax_gw on its events and the actual gateway used can be
// recorded on the job result. Gateway attribution only — the failover order
// and call flow are unchanged.
func endpointGatewayDialstringOutboundTagged(endpoints []string, dstNum string) string {
	var dsGateways string

	for n, gw := range endpoints {
		if n > 0 {
			dsGateways += ","
		}
		dsGateways += fmt.Sprintf("[%s=%v]sofia/gateway/%v/%v",
			outboundGatewayTagVar, gw, gw, dstNum)
	}

	return dsGateways
}
