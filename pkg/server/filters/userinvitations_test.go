package filters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

func TestUserUserInvitationListConstraintDecorator(t *testing.T) {
	testCases := []struct {
		name                  string
		requestPath           string
		apiGroup              string
		resource              string
		verb                  string
		userID                string
		existingFieldSelector string
		expectedFieldSelector string
	}{
		{
			name:                  "userinvitations list with user context",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "",
			expectedFieldSelector: ",status.inviteeUser.name=test-user",
		},
		{
			name:                  "userinvitations list with existing field selector",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "metadata.name=test-invite",
			expectedFieldSelector: ",metadata.name=test-invite,status.inviteeUser.name=test-user",
		},
		{
			name:                  "existing invitee user filter replaced",
			requestPath:           "/apis/iam.miloapis.com/v1alpha1/userinvitations",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "status.inviteeUser.name=other-user",
			expectedFieldSelector: ",status.inviteeUser.name=test-user",
		},
		{
			name:        "non-userinvitations request",
			requestPath: "/api/v1/pods",
			apiGroup:    "",
			resource:    "pods",
			verb:        "list",
			userID:      "test-user",
		},
		{
			name:                  "userinvitations watch with user context",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "watch",
			userID:                "test-user",
			existingFieldSelector: "",
			expectedFieldSelector: ",status.inviteeUser.name=test-user",
		},
		{
			name:                  "userinvitations watch with existing field selector",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "watch",
			userID:                "test-user",
			existingFieldSelector: "metadata.name=test-invite",
			expectedFieldSelector: ",metadata.name=test-invite,status.inviteeUser.name=test-user",
		},
		{
			name:                  "userinvitations watch replaces existing invitee user filter",
			requestPath:           "/apis/iam.miloapis.com/v1alpha1/userinvitations",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "watch",
			userID:                "test-user",
			existingFieldSelector: "status.inviteeUser.name=other-user",
			expectedFieldSelector: ",status.inviteeUser.name=test-user",
		},
		{
			name:        "userinvitations get request",
			requestPath: "/apis/iam.miloapis.com/v1alpha1/userinvitations/test-invite",
			apiGroup:    iamv1alpha1.SchemeGroupVersion.Group,
			resource:    "userinvitations",
			verb:        "get",
			userID:      "test-user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector string

			handler := UserUserInvitationListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if info, ok := request.RequestInfoFrom(req.Context()); ok {
					capturedFieldSelector = info.FieldSelector
				}
			}))

			requestURL := tc.requestPath
			if tc.existingFieldSelector != "" {
				u, _ := url.Parse(requestURL)
				query := u.Query()
				query.Set("fieldSelector", tc.existingFieldSelector)
				u.RawQuery = query.Encode()
				requestURL = u.String()
			}

			req := httptest.NewRequest("GET", "http://localhost"+requestURL, nil)
			ctx := req.Context()

			requestInfo := &request.RequestInfo{
				IsResourceRequest: true,
				APIGroup:          tc.apiGroup,
				Resource:          tc.resource,
				Verb:              tc.verb,
				FieldSelector:     tc.existingFieldSelector,
			}

			ctx = request.WithRequestInfo(ctx, requestInfo)

			if tc.userID != "" {
				ctx = request.WithValue(ctx, UserIDContextKey, tc.userID)
			}

			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tc.expectedFieldSelector != "" {
				if capturedFieldSelector != tc.expectedFieldSelector {
					t.Fatalf("expected field selector %q, got %q", tc.expectedFieldSelector, capturedFieldSelector)
				}
			}
		})
	}
}
