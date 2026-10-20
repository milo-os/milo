package filters

import (
	"fmt"
	"net/http"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
)

const controlPlaneSubresource = "control-plane"

var controlPlaneParents = map[schema.GroupResource]bool{
	{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "projects"}:      true,
	{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "organizations"}: true,
	{Group: iamv1alpha1.SchemeGroupVersion.Group, Resource: "users"}:               true,
}

func WithControlPlanePathNotFound(handler http.Handler, s runtime.NegotiatedSerializer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		info, ok := request.RequestInfoFrom(req.Context())
		if !ok || !info.IsResourceRequest || info.Subresource != controlPlaneSubresource {
			handler.ServeHTTP(w, req)
			return
		}

		gr := schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}
		if !controlPlaneParents[gr] {
			handler.ServeHTTP(w, req)
			return
		}

		err := apierrors.NewNotFound(gr, info.Name)
		err.ErrStatus.Message = fmt.Sprintf(
			"%s/%s/%s is a control-plane path prefix, not an API resource; send each request with the prefix exactly once",
			gr.Resource, info.Name, controlPlaneSubresource,
		)
		gv := schema.GroupVersion{Group: info.APIGroup, Version: info.APIVersion}
		responsewriters.ErrorNegotiated(err, s, gv, w, req)
	})
}
