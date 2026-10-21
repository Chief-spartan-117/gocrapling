// Package api embeds the OpenAPI document that describes the REST API.
// The spec is the contract: change api/openapi.json together with
// internal/rest, and regenerate or update the clients under sdk/.
package api

import _ "embed"

// OpenAPI is the OpenAPI 3 document served at GET /v1/openapi.json.
//
//go:embed openapi.json
var OpenAPI []byte
