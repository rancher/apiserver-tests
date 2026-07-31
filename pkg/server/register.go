package server

import (
	"net/http"

	"github.com/rancher/apiserver-tests/pkg/store/engine"
	"github.com/rancher/apiserver/pkg/server"
	"github.com/rancher/apiserver/pkg/store/apiroot"
	"github.com/rancher/apiserver/pkg/subscribe"
	"github.com/rancher/apiserver/pkg/types"
	"github.com/rancher/wrangler/v3/pkg/schemas"
)

type Widget struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
}

func BuildAPIHandler() (http.Handler, error) {
	s := server.DefaultAPIServer()

	// Establish the API base root routing namespace
	apiroot.Register(s.Schemas, []string{"v1"})

	// Register watch endpoints.
	// Passing 'nil' tells the framework to use the default schema group.
	subscribe.Register(s.Schemas, nil, "v1")

	// Create a shared in-memory store for the "widget" resource.
	sharedStorage := engine.NewMemoryStore()

	// Import the struct and customize the schema definitions
	s.Schemas.MustImportAndCustomize(Widget{}, func(schema *types.APISchema) {
		schema.Store = sharedStorage
		schema.CollectionMethods = []string{http.MethodGet, http.MethodPost}
		schema.ResourceMethods = []string{http.MethodGet, http.MethodDelete, http.MethodPut}

		if schema.Schema == nil {
			schema.Schema = &schemas.Schema{}
		}
		schema.Schema.ID = "widget"
		schema.Schema.PluralName = "widgets"
	})

	// Create router multiplexer using standard wildcards.
	// This matches the rancher/apiserver architectural pattern.
	mux := http.NewServeMux()
	
	// Both /v1/widgets AND /v1/subscribe will hit this route.
	// For /v1/widgets:   prefix="v1", type="widgets"
	// For /v1/subscribe: prefix="v1", type="subscribe"
	mux.Handle("/{prefix}/{type}", s)
	mux.Handle("/{prefix}/{type}/{name}", s)

	return AuditLoggingMiddleware(mux), nil
}