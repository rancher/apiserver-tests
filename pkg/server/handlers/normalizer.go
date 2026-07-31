package handlers

import (
	"net/http"

	"github.com/rancher/apiserver/pkg/apierror"
	"github.com/rancher/apiserver/pkg/types"
	"github.com/rancher/wrangler/v3/pkg/schemas/validation"
)

func WidgetValidator(request *types.APIRequest, schema *types.APISchema, data types.APIObject) error {
	if request.Method == http.MethodPost {
		dataMap := data.Data()
		profile, _ := dataMap["profile"].(string)
		if profile != "production" && profile != "staging" {
			return apierror.NewAPIError(validation.InvalidBodyContent, "Field 'profile' must be 'production' or 'staging'")
		}
	}
	return nil
}

func WidgetFormatter(request *types.APIRequest, resource *types.RawResource) {
	resource.Links["reconcile"] = request.URLBuilder.ResourceLink(resource.Schema, resource.ID) + "/reconcile"
	
	dataMap := resource.APIObject.Data()
	dataMap["managedBy"] = "suse-rancher-apiserver-playground"
}