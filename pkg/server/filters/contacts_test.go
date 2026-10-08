package filters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apiserver/pkg/endpoints/request"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
)

func TestUserContactListConstraintDecorator(t *testing.T) {
	testCases := []struct {
		name                   string
		requestPath            string
		apiGroup               string
		resource               string
		verb                   string
		userID                 string
		existingFieldSelector  string
		expectedFieldSelectors []string // Use list because map iteration order is random in selector string
	}{
		{
			name:                   "contacts list with user context",
			apiGroup:               notificationv1alpha1.SchemeGroupVersion.Group,
			resource:               "contacts",
			verb:                   "list",
			userID:                 "test-user",
			existingFieldSelector:  "",
			expectedFieldSelectors: []string{"spec.subject.name=test-user", "spec.subject.kind=User"},
		},
		{
			name:                   "contacts list with existing field selector",
			apiGroup:               notificationv1alpha1.SchemeGroupVersion.Group,
			resource:               "contacts",
			verb:                   "list",
			userID:                 "test-user",
			existingFieldSelector:  "metadata.name=test-contact",
			expectedFieldSelectors: []string{"metadata.name=test-contact", "spec.subject.name=test-user", "spec.subject.kind=User"},
		},
		{
			name:                   "existing subject name filter replaced",
			requestPath:            "/apis/notification.miloapis.com/v1alpha1/contacts",
			apiGroup:               notificationv1alpha1.SchemeGroupVersion.Group,
			resource:               "contacts",
			verb:                   "list",
			userID:                 "test-user",
			existingFieldSelector:  "spec.subject.name=other-user",
			expectedFieldSelectors: []string{"spec.subject.name=test-user", "spec.subject.kind=User"},
		},
		{
			name:                   "existing subject kind filter replaced",
			requestPath:            "/apis/notification.miloapis.com/v1alpha1/contacts",
			apiGroup:               notificationv1alpha1.SchemeGroupVersion.Group,
			resource:               "contacts",
			verb:                   "list",
			userID:                 "test-user",
			existingFieldSelector:  "spec.subject.kind=Group",
			expectedFieldSelectors: []string{"spec.subject.name=test-user", "spec.subject.kind="},
		},
		{
			name:        "non-contacts request",
			requestPath: "/api/v1/pods",
			apiGroup:    "",
			resource:    "pods",
			verb:        "list",
			userID:      "test-user",
		},
		{
			name:        "contacts get request",
			requestPath: "/apis/notification.miloapis.com/v1alpha1/contacts/test-contact",
			apiGroup:    notificationv1alpha1.SchemeGroupVersion.Group,
			resource:    "contacts",
			verb:        "get",
			userID:      "test-user",
		},
	}

	// Mock resources in our "database"
	mockResources := []struct {
		Name        string
		SubjectName string
		SubjectKind string
	}{
		{Name: "contact-1", SubjectName: "test-user", SubjectKind: "User"},
		{Name: "contact-2", SubjectName: "other-user", SubjectKind: "User"},
		{Name: "contact-3", SubjectName: "test-user", SubjectKind: "Group"}, // Should not match
	}

	// A watch must be constrained exactly like a list, or it streams every
	// user's objects. Re-run each list case as a watch.
	for _, tc := range testCases {
		if tc.verb == "list" && tc.resource == "contacts" {
			tc.name += " (watch)"
			tc.verb = "watch"
			testCases = append(testCases, tc)
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector string

			handler := UserContactListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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

			if len(tc.expectedFieldSelectors) > 0 {
				for _, expected := range tc.expectedFieldSelectors {
					if !strings.Contains(capturedFieldSelector, expected) {
						t.Errorf("expected field selector to contain %q, got %q", expected, capturedFieldSelector)
					}
				}

				// Simulate database retrieval
				if tc.verb == "list" && tc.userID != "" {
					s, err := fields.ParseSelector(capturedFieldSelector)
					if err != nil {
						// t.Errorf("failed to parse selector: %v", err)
					} else {
						for _, r := range mockResources {
							if s.Matches(fields.Set{"spec.subject.name": r.SubjectName, "spec.subject.kind": r.SubjectKind, "metadata.name": r.Name}) {
								if r.SubjectName != tc.userID {
									t.Errorf("Security Flaw: Filter allowed retrieving contact %s belonging to %s, but authenticated user is %s", r.Name, r.SubjectName, tc.userID)
								}
								if r.SubjectKind != "User" {
									t.Errorf("Security Flaw: Filter allowed retrieving contact %s with kind %s, expected User", r.Name, r.SubjectKind)
								}
							}
						}
					}
				}
			}
		})
	}
}
