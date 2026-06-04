package filters

import (
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const (
	// UserInvitationInviteeUserFieldSelector is the field selector for the invitee user in a user invitation.
	// This field contains the name of the invitee user in the invitation.
	// Some invites may not have a created invitee user yet, so this field may be empty.
	UserInvitationInviteeUserFieldSelector = "status.inviteeUser.name"
)

// UserUserInvitationListConstraintDecorator intercepts requests to list
// user invitations, which are a user scoped resource, and injects a field
// selector to limit user invitations to the user provided in the request
// context.
//
// This is done so that end users can execute `kubectl get userinvitations`
// and not need to provide a field selector.
func UserUserInvitationListConstraintDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup == iamv1alpha1.SchemeGroupVersion.Group && info.Resource == "userinvitations" && (info.Verb == "list" || info.Verb == "watch") {
			userID, ok := ctx.Value(UserIDContextKey).(string)
			if ok {
				currentSelector, err := fields.ParseSelector(info.FieldSelector)
				if err != nil {
					responsewriters.InternalError(w, req, fmt.Errorf("failed to parse label selector: %w", err))
					return
				}

				// Filter out any invitee user constraints that may have been provided
				// in the request by rebuilding the selector without them.
				filteredSelector := fields.Nothing()
				for _, r := range currentSelector.Requirements() {
					if r.Field == UserInvitationInviteeUserFieldSelector {
						// Skip any pre-existing invitee user constraint so we can
						// replace it with the authenticated user's ID.
						continue
					}
					filteredSelector = fields.AndSelectors(filteredSelector, fields.OneTermEqualSelector(r.Field, r.Value))
				}

				// Combine the filtered selector with the new invitee user requirement.
				currentSelector = filteredSelector

				// Build new selector, filtering out any user-id constraint that
				// may have been provided in the request
				newSelector := fields.AndSelectors(currentSelector, fields.SelectorFromSet(fields.Set{
					UserInvitationInviteeUserFieldSelector: userID,
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
