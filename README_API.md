# Fax Server API Documentation

This document details the comprehensive RESTful API provided by gofaxserver. The API is divided into two primary groups:

1. **Admin Endpoints** – For system administrators to manage tenants, tenant numbers, endpoints, and other configuration data.
2. **Tenant Endpoints** – For tenant accounts to authenticate, manage their profiles, and send/receive fax operations.

## Overview

gofaxserver offers a modern approach to Fax over IP by directly interfacing with FreeSWITCH through its Event Socket interface. All data is stored in a PostgreSQL database and interactions are logged via Loki and syslog.

## Authentication

### Admin API

- **Method:** Basic Authentication using an internal API key.
- **Usage:** The authorization header must be set to:

  ```
  Authorization: Basic <base64_encode("anyuser:YOUR_INTERNAL_API_KEY")>
  ```

### Tenant API

- **Method:** Basic Authentication using the tenant user's username and password.
- **Usage:** The authorization header must be set to:

  ```
  Authorization: Basic <base64_encode("tenant_username:tenant_password")>
  ```

> **Note:** For production environments, ensure that sensitive data such as passwords are hashed and that secure protocols (e.g., HTTPS) are enforced.

## Data Models

### Tenant

Represents an organization or department.

```json
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
```

### TenantUser

A user associated with a Tenant who can send faxes.

```json
{
  "id": 1,
  "tenant_id": 1,
  "username": "tenantuser",
  "password": "encrypted_password_here",
  "api_key": "user_api_key"
}
```

### TenantNumber

A phone number assigned to a tenant.

```json
{
  "id": 1,
  "tenant_id": 1,
  "number": "5551234567",
  "notify_emails": "notify@acme.com",
  "cid": "+15551234567",
  "webhook": "Acme Fax Relay"
}
```

### Endpoint

Represents a connection point for fax operations. Endpoints can be of type `tenant`, `number`, or `global`.

```json
{
  "id": 1,
  "type": "tenant",          // valid values: "tenant", "number", or "global"
  "type_id": 1,              // For tenant endpoints, this is the tenant ID; for number endpoints, it's the TenantNumber ID
  "endpoint_type": "gateway", // e.g. "gateway", "webhook", "email"
  "endpoint": "pbx_acme",    // The identifier for the gateway or the actual webhook URL
  "priority": 0
}
```

## API Endpoints

### Admin Endpoints

All admin endpoints expect Basic Authentication using the internal API key.

#### 1. Tenant Management

- **Add Tenant**

  - **Method:** POST
  - **Endpoint:** `/admin/tenant`
  - **Payload:**

    ```json
    {
      "name": "Acme Corp",
      "email": "contact@acme.com"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/admin/tenant \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"name":"Acme Corp","email":"contact@acme.com"}'
    ```

- **Update Tenant**

  - **Method:** PUT
  - **Endpoint:** `/admin/tenant/{id}`
  - **Payload:**

    ```json
    {
      "name": "Acme Corporation",
      "email": "support@acme.com"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X PUT http://localhost:8080/admin/tenant/1 \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"name":"Acme Corporation","email":"support@acme.com"}'
    ```

- **Delete Tenant**

  - **Method:** DELETE
  - **Endpoint:** `/admin/tenant/{id}`

  - **Example cURL:**

    ```bash
    curl -X DELETE http://localhost:8080/admin/tenant/1 \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"
    ```

#### 2. Tenant Number Management

- **Add Tenant Number**

  - **Method:** POST
  - **Endpoint:** `/admin/number`
  - **Payload:**

    ```json
    {
      "tenant_id": 1,
      "number": "5551234567",
      "notify_emails": "notify@acme.com",
      "cid": "+15551234567",
      "webhook": "Acme Fax Relay"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/admin/number \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"tenant_id":1,"number":"5551234567","notify_emails":"notify@acme.com","cid":"+15551234567","webhook":"Acme Fax Relay"}'
    ```

- **Update Tenant Number**

  - **Method:** PUT
  - **Endpoint:** `/admin/number/{id}`
  - **Payload:**

    ```json
    {
      "tenant_id": 1,
      "number": "5551234567",
      "notify_emails": "alert@acme.com",
      "cid": "+15551234567",
      "webhook": "Acme Fax Relay Updated"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X PUT http://localhost:8080/admin/number/1 \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"tenant_id":1,"number":"5551234567","notify_emails":"alert@acme.com","cid":"+15551234567","webhook":"Acme Fax Relay Updated"}'
    ```

- **Delete Tenant Number**

  - **Method:** DELETE
  - **Endpoint:** `/admin/number`
  - **Query Parameters:** `number=5551234567&tenant_id=1`

  - **Example cURL:**

    ```bash
    curl -X DELETE "http://localhost:8080/admin/number?number=5551234567&tenant_id=1" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"
    ```

#### 3. Endpoint Management

- **Add Endpoint**

  - **Method:** POST
  - **Endpoint:** `/admin/endpoint`
  - **Payload:**

    ```json
    {
      "type": "tenant",
      "type_id": 1,
      "endpoint_type": "gateway",
      "endpoint": "pbx_acme",
      "priority": 0
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/admin/endpoint \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"type":"tenant","type_id":1,"endpoint_type":"gateway","endpoint":"pbx_acme","priority":0}'
    ```

- **Update Endpoint**

  - **Method:** PUT
  - **Endpoint:** `/admin/endpoint/{id}`
  - **Payload:**

    ```json
    {
      "type": "tenant",
      "type_id": 1,
      "endpoint_type": "gateway",
      "endpoint": "pbx_acme_updated",
      "priority": 1
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X PUT http://localhost:8080/admin/endpoint/1 \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"type":"tenant","type_id":1,"endpoint_type":"gateway","endpoint":"pbx_acme_updated","priority":1}'
    ```

- **Delete Endpoint**

  - **Method:** DELETE
  - **Endpoint:** `/admin/endpoint/{id}`

  - **Example cURL:**

    ```bash
    curl -X DELETE http://localhost:8080/admin/endpoint/1 \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"
    ```

#### 4. Tenant User Management

- **Add Tenant User**

  - **Method:** POST
  - **Endpoint:** `/admin/user`
  - **Payload:**

    ```json
    {
      "tenant_id": 1,
      "username": "tenantuser",
      "password": "newpassword",
      "api_key": "generated_api_key"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/admin/user \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"tenant_id":1,"username":"tenantuser","password":"newpassword","api_key":"generated_api_key"}'
    ```

- **Update Tenant User**

  - **Method:** PUT
  - **Endpoint:** `/admin/user/{id}`
  - **Payload:**

    ```json
    {
      "tenant_id": 1,
      "username": "updatedtenantuser",
      "password": "updatedpassword",
      "api_key": "updated_api_key"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X PUT http://localhost:8080/admin/user/1 \
      -H "Content-Type: application/json" \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)" \
      -d '{"tenant_id":1,"username":"updatedtenantuser","password":"updatedpassword","api_key":"updated_api_key"}'
    ```

- **Delete Tenant User**

  - **Method:** DELETE
  - **Endpoint:** `/admin/user/{id}`

  - **Example cURL:**

    ```bash
    curl -X DELETE http://localhost:8080/admin/user/1 \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"
    ```

#### 5. Miscellaneous Admin Operations

- **Reload Configuration Data**

  - **Method:** GET
  - **Endpoint:** `/admin/reload`

  - **Purpose:** Reloads tenant, number, and endpoint data from the database into the server's in-memory cache.

  - **Example cURL:**

    ```bash
    curl -X GET http://localhost:8080/admin/reload \
      -H "Authorization: Basic $(echo -n 'admin:YOUR_INTERNAL_API_KEY' | base64)"
    ```

### Tenant Endpoints

These endpoints are for tenant users to perform operational tasks like authentication and fax transmission.

#### 1. Tenant User Authentication

- **Authenticate Tenant User**

  - **Method:** POST
  - **Endpoint:** `/tenant/user/authenticate`

  - **Headers:** Use Basic Authentication with the tenant username and password.

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/tenant/user/authenticate \
      -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)"
    ```

  - **Successful Response:**

    ```json
    {
      "message": "authentication successful",
      "api_key": "user_api_key",
      "user_id": 1,
      "tenant_id": 1
    }
    ```

#### 2. Fax Operations

- **Send Fax**

  - **Method:** POST
  - **Endpoint:** `/fax/send`
  - **Headers:** Use Basic Authentication with the tenant user credentials.
  - **Payload:**

    ```json
    {
      "from_number": "5551234567",
      "to_number": "5559876543",
      "file_name": "/path/to/document.tiff"
    }
    ```

  - **Example cURL:**

    ```bash
    curl -X POST http://localhost:8080/fax/send \
      -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)" \
      -H "Content-Type: application/json" \
      -d '{"from_number":"5551234567","to_number":"5559876543","file_name":"/path/to/document.tiff"}'
    ```

- **Check Fax Status**

  - **Method:** GET
  - **Endpoint:** `/fax/status`
  - **Query Parameter:** `uuid` (the unique ID of the fax job)

  - **Example cURL:**

    ```bash
    curl -X GET "http://localhost:8080/fax/status?uuid=<job_uuid>" \
      -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)"
    ```

## Error Handling & Logging

- Unauthorized requests return HTTP status **401 Unauthorized**.
- Malformed requests return HTTP status **400 Bad Request**.
- Server errors return HTTP status **500 Internal Server Error**.

All errors are logged using the configured LogManager. Adjust the logging level and format as needed in production.

## Additional Notes

- **Validation:** When sending faxes, the API validates that the “from_number” is registered to the tenant.
- **Encryption:** The sample password encryption method in these examples uses a placeholder (Base64 with a salt). Replace this with a secure method (e.g., bcrypt) for production use.
- **Database Setup:** Ensure that the PostgreSQL database is initialized and migrations for models (Tenant, TenantUser, TenantNumber, Endpoint) are applied before starting the server.

## Conclusion

This detailed API documentation should help developers integrate and interact with gofaxserver effectively for both administrative and tenant operations. Adjust endpoint URLs, payloads, and configuration details to suit your deployment environment.
