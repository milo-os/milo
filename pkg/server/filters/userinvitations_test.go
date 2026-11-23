package filters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

func staticUserEmails(emails map[string]string) UserEmailLookup {
	return func(_ context.Context, name string) (string, bool, error) {
		email, ok := emails[name]
		return email, ok, nil
	}
}

func TestUserUserInvitationListConstraintDecorator(t *testing.T) {
	lookup := staticUserEmails(map[string]string{
		"test-user": "Test.User@Example.com",
		"no-email":  "",
	})
	testUserHash := iamv1alpha1.InviteeEmailHash("test.user@example.com")

	testCases := []struct {
		name                  string
		requestPath           string
		apiGroup              string
		resource              string
		verb                  string
		userID                string
		existingFieldSelector string
		expectedFieldSelector string
		expectPassthrough     bool
	}{
		{
			name:                  "list with user context",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			expectedFieldSelector: ",status.inviteeEmailHash=" + testUserHash,
		},
		{
			name:                  "list keeps other requirements",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "metadata.name=test-invite",
			expectedFieldSelector: ",metadata.name=test-invite,status.inviteeEmailHash=" + testUserHash,
		},
		{
			name:                  "invitee email hash requirement replaced",
			requestPath:           "/apis/iam.miloapis.com/v1alpha1/userinvitations",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "status.inviteeEmailHash=" + iamv1alpha1.InviteeEmailHash("other@example.com"),
			expectedFieldSelector: ",status.inviteeEmailHash=" + testUserHash,
		},
		{
			name:                  "retired invitee user requirement dropped",
			requestPath:           "/apis/iam.miloapis.com/v1alpha1/userinvitations",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "watch",
			userID:                "test-user",
			existingFieldSelector: "status.inviteeUser.name=other-user",
			expectedFieldSelector: ",status.inviteeEmailHash=" + testUserHash,
		},
		{
			name:                  "watch with user context",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "watch",
			userID:                "test-user",
			expectedFieldSelector: ",status.inviteeEmailHash=" + testUserHash,
		},
		{
			name:                  "missing user matches nothing",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "ghost",
			expectedFieldSelector: ",status.inviteeEmailHash=none",
		},
		{
			name:                  "user without email matches nothing",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			userID:                "no-email",
			expectedFieldSelector: ",status.inviteeEmailHash=none",
		},
		{
			name:                  "organization scope list untouched",
			apiGroup:              "iam.miloapis.com",
			resource:              "userinvitations",
			verb:                  "list",
			existingFieldSelector: "status.inviteeUser.name=someone",
			expectedFieldSelector: "status.inviteeUser.name=someone",
			expectPassthrough:     true,
		},
		{
			name:              "non-userinvitations request",
			requestPath:       "/api/v1/pods",
			resource:          "pods",
			verb:              "list",
			userID:            "test-user",
			expectPassthrough: true,
		},
		{
			name:              "get request",
			requestPath:       "/apis/iam.miloapis.com/v1alpha1/userinvitations/test-invite",
			apiGroup:          iamv1alpha1.SchemeGroupVersion.Group,
			resource:          "userinvitations",
			verb:              "get",
			userID:            "test-user",
			expectPassthrough: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector, capturedQuerySelector string
			called := false

			handler := UserUserInvitationListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				called = true
				if info, ok := request.RequestInfoFrom(req.Context()); ok {
					capturedFieldSelector = info.FieldSelector
				}
				capturedQuerySelector = req.URL.Query().Get("fieldSelector")
			}), lookup)

			requestURL := tc.requestPath
			if tc.existingFieldSelector != "" {
				u, _ := url.Parse(requestURL)
				query := u.Query()
				query.Set("fieldSelector", tc.existingFieldSelector)
				u.RawQuery = query.Encode()
				requestURL = u.String()
			}

			req := httptest.NewRequest("GET", "http://localhost"+requestURL, nil)
			ctx := request.WithRequestInfo(req.Context(), &request.RequestInfo{
				IsResourceRequest: true,
				APIGroup:          tc.apiGroup,
				Resource:          tc.resource,
				Verb:              tc.verb,
				FieldSelector:     tc.existingFieldSelector,
			})
			if tc.userID != "" {
				ctx = request.WithValue(ctx, UserIDContextKey, tc.userID)
			}

			handler.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

			if !called {
				t.Fatalf("next handler was not called")
			}
			if tc.expectPassthrough {
				if capturedFieldSelector != tc.existingFieldSelector {
					t.Fatalf("expected field selector untouched %q, got %q", tc.existingFieldSelector, capturedFieldSelector)
				}
				return
			}
			if capturedFieldSelector != tc.expectedFieldSelector {
				t.Fatalf("expected field selector %q, got %q", tc.expectedFieldSelector, capturedFieldSelector)
			}
			if capturedQuerySelector != tc.expectedFieldSelector {
				t.Fatalf("expected query field selector %q, got %q", tc.expectedFieldSelector, capturedQuerySelector)
			}
		})
	}
}

func TestUserUserInvitationListConstraintDecorator_LookupError(t *testing.T) {
	called := false
	handler := UserUserInvitationListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
	}), func(context.Context, string) (string, bool, error) {
		return "", false, errors.New("boom")
	})

	req := httptest.NewRequest("GET", "http://localhost/apis/iam.miloapis.com/v1alpha1/userinvitations", nil)
	ctx := request.WithRequestInfo(req.Context(), &request.RequestInfo{
		IsResourceRequest: true,
		APIGroup:          "iam.miloapis.com",
		Resource:          "userinvitations",
		Verb:              "list",
	})
	ctx = request.WithValue(ctx, UserIDContextKey, "test-user")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req.WithContext(ctx))

	if called {
		t.Fatalf("next handler must not run when the User lookup fails")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
