package gofaxserver

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"strings"
)

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
	defer s.mu.Unlock()

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

			// todo parse / add resolved IP
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

	return fmt.Errorf("endpoint with id %d not found", endpointID)
}

func (s *Server) fsGatewayACL(ip string) (string, error) {
	for _, k := range s.GatewayEndpointsACL {
		if strings.Contains(k, ip) {
			return k, nil
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
