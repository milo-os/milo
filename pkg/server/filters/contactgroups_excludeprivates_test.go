package filters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"k8s.io/apiserver/pkg/endpoints/request"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
)

func TestContactGroupVisibilityWithoutPrivateDecorator(t *testing.T) {
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
			name:                  "contactgroups list with user context",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroups",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "",
			expectedFieldSelector: ",spec.visibility=public",
		},
		{
			name:                  "contactgroups list with existing field selector",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroups",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "metadata.name=test-group",
			expectedFieldSelector: ",metadata.name=test-group,spec.visibility=public",
		},
		{
			name:                  "existing visibility filter replaced",
			requestPath:           "/apis/notification.miloapis.com/v1alpha1/contactgroups",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroups",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "spec.visibility=private",
			expectedFieldSelector: ",spec.visibility=public",
		},
		{
			name:        "non-contactgroups request",
			requestPath: "/api/v1/pods",
			apiGroup:    "",
			resource:    "pods",
			verb:        "list",
			userID:      "test-user",
		},
		{
			name:        "contactgroups get request",
			requestPath: "/apis/notification.miloapis.com/v1alpha1/contactgroups/test-group",
			apiGroup:    notificationv1alpha1.SchemeGroupVersion.Group,
			resource:    "contactgroups",
			verb:        "get",
			userID:      "test-user",
		},
		{
			name:                  "contactgroups list without user context",
			apiGroup:              notificationv1alpha1.SchemeGroupVersion.Group,
			resource:              "contactgroups",
			verb:                  "list",
			userID:                "",
			existingFieldSelector: "",
			expectedFieldSelector: "", // Should not be modified
		},
	}

	// A watch must be constrained exactly like a list, or it streams every
	// user's objects. Re-run each list case as a watch.
	for _, tc := range testCases {
		if tc.verb == "list" && tc.resource == "contactgroups" {
			tc.name += " (watch)"
			tc.verb = "watch"
			testCases = append(testCases, tc)
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector string

			handler := ContactGroupVisibilityWithoutPrivateDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
			} else if tc.expectedFieldSelector == "" {
				if capturedFieldSelector != tc.existingFieldSelector {
					t.Fatalf("expected field selector to be unchanged %q, got %q", tc.existingFieldSelector, capturedFieldSelector)
				}
			}
		})
	}
}
