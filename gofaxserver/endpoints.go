package gofaxserver

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
	EndpointType string `json:"endpoint_type"` // webhook, or gateway (gateway is for freeswitch)
	Endpoint     string `json:"endpoint"`      // if type=gateway then this is the freeswitch gateway name, otherwise, it's the webhook url
	Priority     uint   `json:"webhook"`       // priority from 0 to any? 0 being the highest priority
}
