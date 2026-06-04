// Copyright 2024 The Milo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// OrganizationMembershipUserFieldSelector is the field selector for the user in an organization membership.
	OrganizationMembershipUserFieldSelector = "spec.userRef.name"
)

const (
	UserIDContextKey = "userID"
)

// UserID returns the user ID stashed on the request context by
// UserContextHandler, if any. Used by middleware that needs to detect whether
// the current request is being made in the context of a user.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(UserIDContextKey).(string)
	return id, ok
}

// UserContextHandler will react to requests sent to a pseudo API path of
// `/apis/iam.miloapis.com/v1alpha1/users/` and injects the provided user ID
// into a request context value. This value will then be used by
// `UserContextAuthorizationDecorator` to inject the user ID into the
// authenticated user's Extra field.
//
// It will then rewrite the request path to strip the prefix of
// `/apis/iam.miloapis.com/v1alpha1/users/{user}/control-plane`, which will
// result in the next set of handlers seeing a typical API request.
func UserContextHandler(handler http.Handler, s runtime.NegotiatedSerializer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		const prefix = "/apis/iam.miloapis.com/v1alpha1/users/"
		if strings.HasPrefix(req.URL.Path, prefix) {
			// Extract the organization ID and the remaining path
			rest := strings.TrimPrefix(req.URL.Path, prefix)
			parts := strings.SplitN(rest, "/", 2)

			// Set the group version for the response based on the iam
			// API scheme.
			gv := iamv1alpha1.SchemeGroupVersion

			userID := parts[0]

			if errs := validation.IsValidLabelValue(userID); len(errs) > 0 {
				// Return a text/plain response for discovery so that kubectl
				// prints a useful error. If a structured response is given, it will
				// swallow all useful error information.
				if strings.HasSuffix(req.URL.Path, "control-plane/api") {
					w.Header().Add("Content-Type", "text/plain")
					w.WriteHeader(http.StatusForbidden)
					if _, err := w.Write([]byte(fmt.Sprintf("invalid user ID %q", userID))); err != nil {
						responsewriters.InternalError(w, req, fmt.Errorf("failed to write response: %w", err))
					}
				} else {
					responsewriters.ErrorNegotiated(apierrors.NewBadRequest(
						fmt.Sprintf("invalid user ID %q", userID),
					), s, gv, w, req)
				}
				return
			}

			ctx := context.WithValue(req.Context(), UserIDContextKey, userID)
			req = req.WithContext(ctx)

			// Check to see if the request is a direct request for the user
			// resource or the status subresource. If so, we need to allow the request
			// to continue without any additional processing.
			if len(parts) == 1 || (len(parts) == 2 && parts[1] == "status") {
				handler.ServeHTTP(w, req)
				return
			}

			remainingPath := strings.TrimPrefix(parts[1], "control-plane")

			req.URL.Path = strings.SplitN(remainingPath, "?", 2)[0]

		}
		handler.ServeHTTP(w, req)
	})
}

// UserContextAuthorizationDecorator needs to run after authentication, but
// prior to authorization.
//
// This handler injects user information into the authenticated user's Extra
// information that's made available in the request context by the
// `UserContextHandler` handler.
func UserContextAuthorizationDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		userID, ok := ctx.Value(UserIDContextKey).(string)
		if !ok {
			// Not a user scoped request
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

		extra := map[string][]string{
			iamv1alpha1.ParentAPIGroupExtraKey: {iamv1alpha1.SchemeGroupVersion.Group},
			iamv1alpha1.ParentKindExtraKey:     {"User"},
			iamv1alpha1.ParentNameExtraKey:     {userID},
		}

		req = req.WithContext(request.WithUser(ctx, userWithExtra(u, extra)))

		handler.ServeHTTP(w, req)
	})
}

// UserOrganizationListConstraintDecorator intercepts requests to list
// organization memberships, which are an organization scoped resource, and
// injects a field selector to limit organization memberships to the user
// provided in the request context.
//
// This is done so that end users can execute `kubectl get
// organizationmemberships` and not need to provide a field selector.
func UserOrganizationMembershipListConstraintDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup == resourcemanagerv1alpha1.GroupVersion.Group && info.Resource == "organizationmemberships" && info.Verb == "list" {
			userID, ok := ctx.Value(UserIDContextKey).(string)
			if ok {
				currentSelector, err := fields.ParseSelector(info.FieldSelector)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				// Filter out any user-id constraints that may have been provided
				// in the request.
				newRequirements := fields.Requirements{}
				for _, r := range currentSelector.Requirements() {
					if r.Field == OrganizationMembershipUserFieldSelector {
						continue
					}
					newRequirements = append(newRequirements, r)
				}

				// Build new selector, filtering out any user-id constraint that
				// may have been provided in the request
				newSelector := fields.AndSelectors(currentSelector, fields.SelectorFromSet(fields.Set{
					OrganizationMembershipUserFieldSelector: userID,
				}))

				// Set the new field selector on the request info.
				info.FieldSelector = newSelector.String()

				// Inject the new selector into the request
				query, err := url.ParseQuery(req.URL.RawQuery)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse url query: %w", err))
				}
				query.Del("fieldSelector")
				query.Add("fieldSelector", info.FieldSelector)

				req.URL.RawQuery = query.Encode()
			}
		}

		req = req.WithContext(request.WithRequestInfo(ctx, info))

		handler.ServeHTTP(w, req)
	})
}
