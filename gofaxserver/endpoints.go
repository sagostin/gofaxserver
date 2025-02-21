package gofaxserver

import "fmt"

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
	Type         string `json:"type"`          // tenant, or number
	TypeID       uint   `json:"type_id"`       // id of type, either tenant or number id
	EndpointType string `json:"endpoint_type"` // webhook, gateway (gateway is for freeswitch), or email, or freeswitch (which would be in the format of say /external/sofia/gateway/gatewayname or something else for TDM routing)
	Endpoint     string `json:"endpoint"`      // if type=gateway then this is the freeswitch gateway name, otherwise, it's the webhook url, or email address (or multiple email addresses separated by semi-colons)
	Priority     uint   `json:"priority"`      // priority from 0 to any? 0 being the highest priority
}

// addEndpoint adds a new endpoint. Depending on endpoint.Type, it is stored under the tenant or the number.
func (s *Server) addEndpoint(endpoint *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch endpoint.Type {
	case "tenant":
		// For tenant endpoints, TypeID is the tenant's ID.
		s.tenantEndpoints[endpoint.TypeID] = append(s.tenantEndpoints[endpoint.TypeID], endpoint)
	case "number":
		// For number endpoints, we must first find the tenant number using the TenantNumber ID.
		var numberStr string
		for _, tn := range s.tenantNumbers {
			if tn.ID == endpoint.TypeID {
				numberStr = tn.Number
				break
			}
		}
		if numberStr == "" {
			return fmt.Errorf("tenant number with id %d not found", endpoint.TypeID)
		}
		s.numberEndpoints[numberStr] = append(s.numberEndpoints[numberStr], endpoint)
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
	for tenantID, endpoints := range s.tenantEndpoints {
		for i, ep := range endpoints {
			if ep.ID == endpointID {
				s.tenantEndpoints[tenantID] = append(endpoints[:i], endpoints[i+1:]...)
				return nil
			}
		}
	}

	// Remove from number endpoints.
	for numberStr, endpoints := range s.numberEndpoints {
		for i, ep := range endpoints {
			if ep.ID == endpointID {
				s.numberEndpoints[numberStr] = append(endpoints[:i], endpoints[i+1:]...)
				return nil
			}
		}
	}

	return fmt.Errorf("endpoint with id %d not found", endpointID)
}
