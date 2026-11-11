// pkg/filters/project_router.go
package filters

import (
	"net/http"
	"strings"

	"fmt"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	reqinfo "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	projctx "go.miloapis.com/milo/pkg/request"
)

func ProjectRouterWithRequestInfo(next http.Handler, rir reqinfo.RequestInfoResolver) http.Handler {
	const (
		projectsSeg     = "/projects/"
		controlPlaneSeg = "/control-plane"
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only intercept requests that target the valid parent API group/version:
		// /apis/<resourcemanagerGV>/projects/<id>/control-plane/...
		parentPrefix := "/apis/" + resourcemanagerv1alpha1.GroupVersion.String() + projectsSeg
		if !strings.HasPrefix(r.URL.Path, parentPrefix) {
			next.ServeHTTP(w, r)
			return
		}

		tail := strings.TrimPrefix(r.URL.Path, parentPrefix) // "<id>/control-plane/..."
		slash := strings.IndexByte(tail, '/')
		if slash < 0 || !strings.HasPrefix(tail[slash:], controlPlaneSeg+"/") {
			next.ServeHTTP(w, r)
			return
		}

		projID := tail[:slash]

		// Drop ".../projects/<id>/control-plane"
		newPath := "/" + strings.TrimPrefix(tail[slash+len(controlPlaneSeg):], "/")

		// Clone request, stash project, and rewrite URL bits
		r2 := r.Clone(projctx.WithProject(r.Context(), projID))
		r2.URL.Path = newPath
		r2.URL.RawPath = newPath
		if r.URL.RawQuery != "" {
			r2.RequestURI = newPath + "?" + r.URL.RawQuery
		} else {
			r2.RequestURI = newPath
		}

		// 🔁 Recompute RequestInfo on the rewritten request
		ri, err := rir.NewRequestInfo(r2)
		if err != nil {
			klog.ErrorS(err, "Failed to create RequestInfo for project router")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		r2 = r2.WithContext(reqinfo.WithRequestInfo(r2.Context(), ri))

		klog.InfoS("ProjectRouter",
			"project", projID,
			"newPath", newPath,
			"ns", ri.Namespace,
			"resource", ri.Resource,
			"subresource", ri.Subresource,
			"verb", ri.Verb,
			"isResourceRequest", ri.IsResourceRequest,
		)

		next.ServeHTTP(w, r2)
	})
}

// ProjectContextAuthorizationDecorator needs to run AFTER authentication, BEFORE authorization.
// It injects {ParentAPIGroup, ParentKind, ParentName} for the current project into the user extras.
func ProjectContextAuthorizationDecorator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		// Did the ProjectRouter stash a project id on this request?
		projID, ok := projctx.ProjectID(ctx)
		if !ok || projID == "" {
			// Not a project-scoped request; pass through
			next.ServeHTTP(w, req)
			return
		}

		reqUser, ok := reqinfo.UserFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to extract user info from context"))
			return
		}
		u, ok := reqUser.(*user.DefaultInfo)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("unexpected user.Info type. Expected *user.DefaultInfo, got %T", reqUser))
			return
		}

		// Project takes precedence over Organization for parent scoping
		extra := map[string][]string{
			iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
			iamv1alpha1.ParentKindExtraKey:     {"Project"},
			iamv1alpha1.ParentNameExtraKey:     {projID},
		}

		req = req.WithContext(reqinfo.WithUser(ctx, userWithExtra(u, extra)))
		next.ServeHTTP(w, req)
	})
}
