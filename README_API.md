Below is an example README (in Markdown) that documents your Fax Server API. It covers the data model for tenants, tenant users, tenant numbers, and endpoints, and provides sample curl commands for both admin and tenant operations.

Fax Server API Documentation

This API supports two groups of endpoints:
1.	Admin Endpoints – For internal use by administrators. These endpoints are protected by an internal API key (provided via Basic Authentication using your configured API key). They allow you to manage tenants, tenant numbers, and endpoints.
2.	Tenant Endpoints – For tenant users to authenticate, manage their own user account, and send faxes (as well as check fax status). Tenant endpoints are protected via Basic Authentication using the tenant user’s username and password.

	Note:
In this example, the password encryption is implemented via a placeholder (gofaxlib.Encrypt using Base64 with a salt). In production, you should use a secure password hashing method (e.g. bcrypt).

Data Models

Tenant

{
"id": 1,
"name": "Acme Corp",
"email": "contact@acme.com",
"numbers": [
{
"id": 1,
"tenant_id": 1,
"number": "5551234567",
"notify_emails": "notify@acme.com",
"cid": "+15551234567",
"webhook": "Acme Fax Relay"
}
]
}

TenantUser

{
"id": 1,
"tenant_id": 1,
"username": "tenantuser",
"password": "encrypted_password_here",
"api_key": "user_api_key"
}

TenantNumber

(See Tenant above for an example)

Endpoint

{
"id": 1,
"type": "tenant",         // "tenant", "number", or "global"
"type_id": 1,             // For tenant endpoints, this is the tenant ID; for number endpoints, it's the TenantNumber ID.
"endpoint_type": "gateway", // e.g. "gateway", "webhook", "email"
"endpoint": "pbx_acme",   // For gateways, the FreeSWITCH gateway name (or a webhook URL, etc.)
"priority": 0
}

API Endpoints

Admin Endpoints

All admin endpoints require Basic Authentication using the internal API key. The API key is taken from your configuration (or the API_KEY environment variable). The Authorization header must be set to:

Authorization: Basic <base64("anyuser:YOUR_INTERNAL_API_KEY")>

1. Tenant Management
   •	Add Tenant
   Method: POST /admin/management/tenant
   Payload:

{
"name": "Acme Corp",
"email": "contact@acme.com"
}

Example cURL:

curl -X POST http://localhost:8080/admin/management/tenant \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"name":"Acme Corp","email":"contact@acme.com"}'


	•	Update Tenant
Method: PUT /admin/management/tenant/{id}
Payload:

{
"name": "Acme Corporation",
"email": "support@acme.com"
}

Example cURL:

curl -X PUT http://localhost:8080/admin/management/tenant/1 \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"name":"Acme Corporation","email":"support@acme.com"}'


	•	Delete Tenant
Method: DELETE /admin/management/tenant/{id}
Example cURL:

curl -X DELETE http://localhost:8080/admin/management/tenant/1 \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"



2. Tenant Number Management
   •	Add Tenant Number
   Method: POST /admin/management/tenant/number
   Payload:

{
"tenant_id": 1,
"number": "5551234567",
"notify_emails": "notify@acme.com",
"cid": "+15551234567",
"webhook": "Acme Fax Relay"
}

Example cURL:

curl -X POST http://localhost:8080/admin/management/tenant/number \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"tenant_id":1,"number":"5551234567","notify_emails":"notify@acme.com","cid":"+15551234567","webhook":"Acme Fax Relay"}'


	•	Update Tenant Number
Method: PUT /admin/management/tenant/number/{id}
Payload:

{
"tenant_id": 1,
"number": "5551234567",
"notify_emails": "alert@acme.com",
"cid": "+15551234567",
"webhook": "Acme Fax Relay Updated"
}

Example cURL:

curl -X PUT http://localhost:8080/admin/management/tenant/number/1 \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"tenant_id":1,"number":"5551234567","notify_emails":"alert@acme.com","cid":"+15551234567","webhook":"Acme Fax Relay Updated"}'


	•	Delete Tenant Number
Method: DELETE /admin/management/tenant/number
Query Parameters: number=5551234567&tenant_id=1
Example cURL:

curl -X DELETE "http://localhost:8080/admin/management/tenant/number?number=5551234567&tenant_id=1" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"



3. Endpoint Management
   •	Add Endpoint
   Method: POST /admin/management/endpoint
   Payload:

{
"type": "tenant",
"type_id": 1,
"endpoint_type": "gateway",
"endpoint": "pbx_acme",
"priority": 0
}

Example cURL:

curl -X POST http://localhost:8080/admin/management/endpoint \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"type":"tenant","type_id":1,"endpoint_type":"gateway","endpoint":"pbx_acme","priority":0}'


	•	Update Endpoint
Method: PUT /admin/management/endpoint/{id}
Payload:

{
"type": "tenant",
"type_id": 1,
"endpoint_type": "gateway",
"endpoint": "pbx_acme_updated",
"priority": 1
}

Example cURL:

curl -X PUT http://localhost:8080/admin/management/endpoint/1 \
-H "Content-Type: application/json" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
-d '{"type":"tenant","type_id":1,"endpoint_type":"gateway","endpoint":"pbx_acme_updated","priority":1}'


	•	Delete Endpoint
Method: DELETE /admin/management/endpoint/{id}
Example cURL:

curl -X DELETE http://localhost:8080/admin/management/endpoint/1 \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"



4. Fax Log Retrieval (Admin)
   •	Retrieve Fax Logs by Job UUID
   Method: GET /admin/fax/log?uuid=<job_uuid>
   Example cURL:

curl -X GET "http://localhost:8080/admin/fax/log?uuid=43ef7a37-2a03-430d-a686-49ec5a38540c" \
-H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"

Tenant Endpoints

Tenant User Authentication
•	Authenticate Tenant User
Endpoint: POST /tenant/user/authenticate
Headers:
Use Basic Auth with tenant username and password.
Example cURL:

curl -X POST http://localhost:8080/tenant/user/authenticate \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)"

Response:

{
"message": "authentication successful",
"api_key": "user_api_key",
"user_id": 3,
"tenant_id": 1
}



Tenant User Management
•	Add Tenant User
Endpoint: POST /tenant/user
Headers:
Use Basic Auth with tenant credentials.
Payload:

{
"tenant_id": 1,
"username": "newtenantuser",
"password": "newpassword",
"api_key": "generated_api_key"
}

Example cURL:

curl -X POST http://localhost:8080/tenant/user \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)" \
-H "Content-Type: application/json" \
-d '{"tenant_id":1,"username":"newtenantuser","password":"newpassword","api_key":"generated_api_key"}'


	•	Update Tenant User
Endpoint: PUT /tenant/user/{id}
Payload:

{
"tenant_id": 1,
"username": "updatedtenantuser",
"password": "updatedpassword",
"api_key": "updated_api_key"
}

Example cURL:

curl -X PUT http://localhost:8080/tenant/user/3 \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)" \
-H "Content-Type: application/json" \
-d '{"tenant_id":1,"username":"updatedtenantuser","password":"updatedpassword","api_key":"updated_api_key"}'


	•	Delete Tenant User
Endpoint: DELETE /tenant/user/{id}
Example cURL:

curl -X DELETE http://localhost:8080/tenant/user/3 \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)"


	•	List Tenant Users
Endpoint: GET /tenant/user
Example cURL:

curl -X GET http://localhost:8080/tenant/user \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)"



Fax Sending (Tenant)
•	Send Fax
Endpoint: POST /tenant/fax/send
Headers:
Use Basic Auth with tenant user credentials.
Payload:

{
"from_number": "5551234567",
"to_number": "5559876543",
"file_name": "/path/to/converted.tiff"
}

Example cURL:

curl -X POST http://localhost:8080/tenant/fax/send \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)" \
-H "Content-Type: application/json" \
-d '{"from_number":"5551234567","to_number":"5559876543","file_name":"/path/to/converted.tiff"}'


	•	Check Fax Status
Endpoint: GET /fax/status?uuid=<job_uuid>
Example cURL:

curl -X GET "http://localhost:8080/fax/status?uuid=43ef7a37-2a03-430d-a686-49ec5a38540c" \
-H "Authorization: Basic $(echo -n 'tenantuser:secret' | base64)"

Additional Notes
•	Admin vs. Tenant Authentication:
•	Admin endpoints require the internal API key (provided via the Authorization header using Basic Auth with any username and the API key as password).
•	Tenant endpoints require Basic Auth with the tenant user’s username and password.
•	Validation:
When sending faxes, the API validates that the “from” number is registered for the tenant, ensuring that users can only send from allowed numbers.
•	Database Setup:
Ensure that your Server struct’s DB *gorm.DB is properly initialized and that you run migrations for models: Tenant, TenantNumber, TenantUser, and Endpoint.
•	Encryption:
The password encryption here uses a placeholder (gofaxlib.Encrypt with Base64 encoding). Replace it with a secure method (e.g. bcrypt) for production use.
•	Error Handling & Logging:
The API logs unauthorized attempts and errors using your LogManager. Adjust logging levels and formats as needed.

This README provides comprehensive API documentation along with example curl commands to help developers interact with your Fax Server API for both admin and tenant operations. Adjust endpoint URLs, payload examples, and configuration details to match your deployment environment.