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
	// ContactGroupMembershipRemovalUsernameFieldSelector is the field selector for the username in a contact group membership removal.
	// This field contains the username of the user that owns the membership removal.
	ContactGroupMembershipRemovalUsernameFieldSelector = "status.username"
)

// UserContactGroupMembershipRemovalListConstraintDecorator intercepts requests to list or watch
// contact group membership removals, and injects a field selector to limit them to the user provided in the request context.
//
// This is done so that end users can execute `kubectl get contactgroupmembershipremovals`
// and not need to provide a field selector.
func UserContactGroupMembershipRemovalListConstraintDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup == notificationv1alpha1.SchemeGroupVersion.Group && info.Resource == "contactgroupmembershipremovals" && (info.Verb == "list" || info.Verb == "watch") {
			userID, ok := ctx.Value(UserIDContextKey).(string)
			if ok {
				currentSelector, err := fields.ParseSelector(info.FieldSelector)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				// Filter out any contact constraint that may have been provided
				// in the request by rebuilding the selector without them.
				filteredSelector := fields.Nothing()
				for _, r := range currentSelector.Requirements() {
					if r.Field == ContactGroupMembershipRemovalUsernameFieldSelector {
						// Skip any pre-existing contact constraint so we can
						// replace it with the authenticated user's ID.
						continue
					}
					filteredSelector = fields.AndSelectors(filteredSelector, fields.OneTermEqualSelector(r.Field, r.Value))
				}

				// Combine the filtered selector with the new contact user requirement.
				currentSelector = filteredSelector

				// Build new selector, filtering out any user-id constraint that
				// may have been provided in the request
				newSelector := fields.AndSelectors(currentSelector, fields.SelectorFromSet(fields.Set{
					ContactGroupMembershipRemovalUsernameFieldSelector: userID,
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
