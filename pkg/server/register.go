package server

import (
	"net/http"

	"github.com/rancher/apiserver-tests/pkg/server/handlers"
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
	return BuildAPIHandlerWithAccessControl(nil)
}

func BuildAPIHandlerWithAccessControl(
	accessControl types.AccessControl,
) (http.Handler, error) {
	s := server.DefaultAPIServer()

	// Allow tests to inject a custom access-control policy.
	// A nil value preserves the default schema-based policy.
	if accessControl != nil {
		s.AccessControl = accessControl
	}

	// Establish the API base root routing namespace.
	apiroot.Register(s.Schemas, []string{"v1"})

	// Register watch endpoints.
	// Passing nil tells the framework to use the default schema group.
	subscribe.Register(s.Schemas, nil, "v1")

	// Create a shared in-memory store for the widget resource.
	sharedStorage := engine.NewMemoryStore()

	// Import the struct and customize its API schema.
	s.Schemas.MustImportAndCustomize(Widget{}, func(schema *types.APISchema) {
		schema.Store = sharedStorage
		schema.CollectionMethods = []string{
			http.MethodGet,
			http.MethodPost,
		}
		schema.ResourceMethods = []string{
			http.MethodGet,
			http.MethodDelete,
			http.MethodPut,
		}

		// Apply custom resource formatting to API responses.
		schema.Formatter = handlers.WidgetFormatter

		if schema.Schema == nil {
			schema.Schema = &schemas.Schema{}
		}

		schema.Schema.ID = "widget"
		schema.Schema.PluralName = "widgets"
	})

	// Route API requests through the server.
	mux := http.NewServeMux()

	// /v1/widgets and /v1/subscribe
	mux.Handle("/{prefix}/{type}", s)

	// /v1/widgets/{name}
	mux.Handle("/{prefix}/{type}/{name}", s)

	return AuditLoggingMiddleware(mux), nil
}
