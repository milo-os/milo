package filters

import (
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
)

const (
	// ContactGroupVisibilityFieldSelector is the field selector for the visibility in a contact group.
	ContactGroupVisibilityFieldSelector = "spec.visibility"
)

// ContactGroupVisibilityWithoutPrivateDecorator intercepts requests to list or watch
// contact groups, and injects a field selector to only include public contact groups.
//
// This is done so that end users can execute `kubectl get contactgroups`
// and only see public contact groups.
func ContactGroupVisibilityWithoutPrivateDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup == notificationv1alpha1.SchemeGroupVersion.Group && info.Resource == "contactgroups" && (info.Verb == "list" || info.Verb == "watch") {
			_, ok := ctx.Value(UserIDContextKey).(string)
			if ok {
				currentSelector, err := fields.ParseSelector(info.FieldSelector)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				// Filter out any subject constraints that may have been provided
				// in the request by rebuilding the selector without them.
				filteredSelector := fields.Nothing()
				for _, r := range currentSelector.Requirements() {
					if r.Field == ContactGroupVisibilityFieldSelector {
						// Skip any pre-existing subject constraints so we can
						// replace it with the authenticated user's ID.
						continue
					}
					filteredSelector = fields.AndSelectors(filteredSelector, fields.OneTermEqualSelector(r.Field, r.Value))
				}

				// Combine the filtered selector with the new subject requirements.
				currentSelector = filteredSelector

				// Build new selector, filtering out any user-id/kind constraint that
				// may have been provided in the request
				newSelector := fields.AndSelectors(currentSelector, fields.SelectorFromSet(fields.Set{
					ContactGroupVisibilityFieldSelector: "public",
				}))

				// Set the new field selector on the request info.
				info.FieldSelector = newSelector.String()

				// Inject the new selector into the request
				query, err := url.ParseQuery(req.URL.RawQuery)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse url query: %w", err))
					return
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
