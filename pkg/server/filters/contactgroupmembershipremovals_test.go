package filters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"k8s.io/apiserver/pkg/endpoints/request"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
)

func TestUserContactGroupMembershipRemovalListConstraintDecorator(t *testing.T) {
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
			name:                  "contactgroupmembershipremovals list with user context",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroupmembershipremovals",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "",
			expectedFieldSelector: ",status.username=test-user",
		},
		{
			name:                  "contactgroupmembershipremovals list with existing field selector",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroupmembershipremovals",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "metadata.name=test-removal",
			expectedFieldSelector: ",metadata.name=test-removal,status.username=test-user",
		},
		{
			name:                  "existing contact user filter replaced",
			requestPath:           "/apis/notification.miloapis.com/v1alpha1/contactgroupmembershipremovals",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroupmembershipremovals",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "status.username=other-user",
			expectedFieldSelector: ",status.username=test-user",
		},
		{
			name:        "non-contactgroupmembershipremovals request",
			requestPath: "/api/v1/pods",
			apiGroup:    "",
			resource:    "pods",
			verb:        "list",
			userID:      "test-user",
		},
		{
			name:        "contactgroupmembershipremovals get request",
			requestPath: "/apis/notification.miloapis.com/v1alpha1/contactgroupmembershipremovals/test-removal",
			apiGroup:    notificationv1alpha1.SchemeGroupVersion.Group,
			resource:    "contactgroupmembershipremovals",
			verb:        "get",
			userID:      "test-user",
		},
	}

	// A watch must be constrained exactly like a list, or it streams every
	// user's objects. Re-run each list case as a watch.
	for _, tc := range testCases {
		if tc.verb == "list" && tc.resource == "contactgroupmembershipremovals" {
			tc.name += " (watch)"
			tc.verb = "watch"
			testCases = append(testCases, tc)
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector string

			handler := UserContactGroupMembershipRemovalListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
