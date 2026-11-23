package filters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const (
	// UserInvitationInviteeUserFieldSelector is the retired invitee field
	// selector. User scoped requests may not set it.
	UserInvitationInviteeUserFieldSelector = "status.inviteeUser.name"

	// UserInvitationInviteeEmailHashFieldSelector selects the invitations
	// addressed to an email address, by InviteeEmailHash.
	UserInvitationInviteeEmailHashFieldSelector = "status.inviteeEmailHash"

	noInviteeEmailHash = "none"
)

// UserEmailLookup returns the email of the named User, or found=false when
// the User does not exist.
type UserEmailLookup func(ctx context.Context, userName string) (email string, found bool, err error)

// NewLoopbackUserEmailLookup reads Users through the API server's loopback
// client.
func NewLoopbackUserEmailLookup(loopbackConfig *rest.Config) UserEmailLookup {
	var (
		client dynamic.Interface
		err    error
	)
	if loopbackConfig != nil {
		client, err = dynamic.NewForConfig(loopbackConfig)
	}
	usersGVR := iamv1alpha1.SchemeGroupVersion.WithResource("users")
	return func(ctx context.Context, userName string) (string, bool, error) {
		if client == nil {
			return "", false, fmt.Errorf("no loopback client for User lookups: %v", err)
		}
		u, getErr := client.Resource(usersGVR).Get(ctx, userName, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return "", false, nil
		}
		if getErr != nil {
			return "", false, fmt.Errorf("failed to get User %q: %w", userName, getErr)
		}
		email, _, _ := unstructured.NestedString(u.Object, "spec", "email")
		return email, true, nil
	}
}

// UserUserInvitationListConstraintDecorator intercepts requests to list or
// watch user invitations in a user's context and restricts them to the
// invitations addressed to that user's email.
//
// This is done so that end users can execute `kubectl get userinvitations`
// and not need to provide a field selector. It runs after authorization, so
// only authorized requests cause a User lookup.
func UserUserInvitationListConstraintDecorator(handler http.Handler, lookup UserEmailLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to get RequestInfo from context"))
			return
		}

		if info.APIGroup != iamv1alpha1.SchemeGroupVersion.Group || info.Resource != "userinvitations" || (info.Verb != "list" && info.Verb != "watch") {
			handler.ServeHTTP(w, req)
			return
		}

		userID, ok := ctx.Value(UserIDContextKey).(string)
		if !ok {
			handler.ServeHTTP(w, req)
			return
		}

		currentSelector, err := fields.ParseSelector(info.FieldSelector)
		if err != nil {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to parse field selector: %w", err))
			return
		}

		email, found, err := lookup(ctx, userID)
		if err != nil {
			responsewriters.InternalError(w, req, err)
			return
		}
		hash := noInviteeEmailHash
		if found && email != "" {
			hash = iamv1alpha1.InviteeEmailHash(email)
		}

		filteredSelector := fields.Nothing()
		for _, r := range currentSelector.Requirements() {
			if r.Field == UserInvitationInviteeUserFieldSelector || r.Field == UserInvitationInviteeEmailHashFieldSelector {
				continue
			}
			filteredSelector = fields.AndSelectors(filteredSelector, fields.OneTermEqualSelector(r.Field, r.Value))
		}

		newSelector := fields.AndSelectors(filteredSelector, fields.SelectorFromSet(fields.Set{
			UserInvitationInviteeEmailHashFieldSelector: hash,
		}))

		info.FieldSelector = newSelector.String()

		query, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			responsewriters.InternalError(w, req, fmt.Errorf("failed to parse url query: %w", err))
			return
		}
		query.Del("fieldSelector")
		query.Add("fieldSelector", info.FieldSelector)
		req.URL.RawQuery = query.Encode()

		handler.ServeHTTP(w, req.WithContext(request.WithRequestInfo(ctx, info)))
	})
}
