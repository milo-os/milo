package filters

import (
	"fmt"
	"net/http"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
)

var controlPlaneParentSubresources = map[schema.GroupResource]sets.Set[string]{
	{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "projects"}:      sets.New("status"),
	{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "organizations"}: sets.New("status"),
	{Group: iamv1alpha1.SchemeGroupVersion.Group, Resource: "users"}:               sets.New("status"),
}

func WithUnservedParentSubresourceNotFound(handler http.Handler, s runtime.NegotiatedSerializer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		info, ok := request.RequestInfoFrom(req.Context())
		if !ok || !info.IsResourceRequest || info.Subresource == "" {
			handler.ServeHTTP(w, req)
			return
		}

		gr := schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}
		served, ok := controlPlaneParentSubresources[gr]
		if !ok || served.Has(info.Subresource) {
			handler.ServeHTTP(w, req)
			return
		}

		err := apierrors.NewNotFound(gr, info.Name)
		err.ErrStatus.Message = fmt.Sprintf(
			"%s/%s/%s is not an API path; a control-plane prefix must appear once, as %s/<name>/control-plane/",
			gr.Resource, info.Name, info.Subresource, gr.Resource,
		)
		gv := schema.GroupVersion{Group: info.APIGroup, Version: info.APIVersion}
		responsewriters.ErrorNegotiated(err, s, gv, w, req)
	})
}
