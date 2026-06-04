package filters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
)

type key int

const orgId key = iota

// OrganizationID returns the organization ID stashed on the request context by
// OrganizationContextHandler, if any. Used by middleware that needs to detect
// whether the current request is being made in the context of an organization.
func OrganizationID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(orgId).(string)
	return id, ok
}

// OrganizationContextHandler will react to requests sent to a pseudo API path
// of `/apis/resourcemanager.miloapis.com/v1alpha1/organizations/` and injects
// the provided organization ID into a request context value. This value will
// then be used by `organizationContextAuthorizationDecorator` to inject the
// org ID into the authenticated user's Extra field. It will then rewrite the
// request path to strip the prefix of `/apis/resourcemanager.miloapis.com/v1alpha1/organizations/{organization}/control-plane`,
// which will result in the next set of handlers seeing a typical API request.
func OrganizationContextHandler(handler http.Handler, s runtime.NegotiatedSerializer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		const prefix = "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/"
		if strings.HasPrefix(req.URL.Path, prefix) {
			// Extract the organization ID and the remaining path
			rest := strings.TrimPrefix(req.URL.Path, prefix)
			parts := strings.SplitN(rest, "/", 2)

			// Set the group version for the response based on the resource manager
			// API scheme.
			gv := v1alpha1.GroupVersion

			organizationID := parts[0]

			if errs := validation.IsValidLabelValue(organizationID); len(errs) > 0 || len(organizationID) == 0 {
				// Return a text/plain response for discovery so that kubectl
				// prints a useful error. If a structured response is given, it will
				// swallow all useful error information.
				if strings.HasSuffix(req.URL.Path, "control-plane/api") {
					w.Header().Add("Content-Type", "text/plain")
					w.WriteHeader(http.StatusForbidden)
					if _, err := w.Write([]byte(fmt.Sprintf("invalid organization ID %q", organizationID))); err != nil {
						responsewriters.InternalError(w, req, fmt.Errorf("failed to write response: %w", err))
					}
				} else {
					responsewriters.ErrorNegotiated(apierrors.NewBadRequest(
						fmt.Sprintf("invalid organization ID %q", organizationID),
					), s, gv, w, req)
				}
				return
			}

			ctx := context.WithValue(req.Context(), orgId, organizationID)
			req = req.WithContext(ctx)

			// Check to see if the request is a direct request for the organization
			// resource. If so, we need to allow the request to continue without any
			// additional processing.
			if len(parts) == 1 {
				handler.ServeHTTP(w, req)
				return
			}

			remainingPath := strings.TrimPrefix(parts[1], "control-plane")

			req.URL.Path = strings.SplitN(remainingPath, "?", 2)[0]

		}
		handler.ServeHTTP(w, req)
	})
}

// OrganizationContextAuthorizationDecorator needs to run after authentication,
// but prior to authorization.
//
// This handler injects organization information into the authenticated user's
// Extra information that's made available in the request context by
// the `organizationContextHandler` handler.
func OrganizationContextAuthorizationDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		orgId, ok := ctx.Value(orgId).(string)
		if !ok {
			// Not an org scoped request
			handler.ServeHTTP(w, req)
			return
		}

		reqUser, ok := request.UserFrom(ctx)
		if !ok {
			// error handling
			responsewriters.InternalError(w, req, fmt.Errorf("failed to extract user info from context"))
			return
		}

		u, ok := reqUser.(*user.DefaultInfo)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("unexpected user.Info type. Expected *user.DefaultInfo, got %T", reqUser))
			return
		}

		// Set the parent resource information for the authorization check based on
		// the organization ID that was provided in the request context.
		extra := map[string][]string{
			iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
			iamv1alpha1.ParentKindExtraKey:     {"Organization"},
			iamv1alpha1.ParentNameExtraKey:     {orgId},
		}

		req = req.WithContext(request.WithUser(ctx, userWithExtra(u, extra)))

		handler.ServeHTTP(w, req)
	})
}

// OrganizationProjectListConstraintDecorator intercepts requests to list
// projects, which are a cluster scoped resource, and injects a label selector
// to limit projects to the organization provided in the request context.
//
// This is done so that end users can execute `kubectl get projects` and not
// need to provide a label selector.
func OrganizationProjectListConstraintDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup == "resourcemanager.miloapis.com" && info.Resource == "projects" && info.Verb == "list" {
			organizationID, ok := ctx.Value(orgId).(string)
			if ok {
				requirements, err := labels.ParseToRequirements(info.LabelSelector)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				orgConstraint, err := labels.NewRequirement(v1alpha1.OrganizationNameLabel, selection.Equals, []string{organizationID})
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				// Build new selector, filtering out any organization-uid constraint that
				// may have been provided in the request
				selector := labels.NewSelector()
				selector = selector.Add(*orgConstraint)
				for _, r := range requirements {
					if r.Key() == v1alpha1.OrganizationNameLabel {
						continue
					}
					selector = selector.Add(r)
				}

				info.LabelSelector = selector.String()

				// Inject the new selector into the request
				query, err := url.ParseQuery(req.URL.RawQuery)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse url query: %w", err))
				}
				query.Del("labelSelector")
				query.Add("labelSelector", info.LabelSelector)

				req.URL.RawQuery = query.Encode()
			}
		}

		req = req.WithContext(request.WithRequestInfo(ctx, info))

		handler.ServeHTTP(w, req)
	})
}
