// Copyright 2024 The Milo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func TestUserContextHandler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	codecs := serializer.NewCodecFactory(scheme)

	testCases := []struct {
		name           string
		requestPath    string
		expectedUserID string
		expectError    bool
		expectRewrite  bool
		expectedPath   string
	}{
		{
			name:           "valid user path with control-plane",
			requestPath:    "/apis/iam.miloapis.com/v1alpha1/users/test-user/control-plane/apis/resourcemanager.miloapis.com/organizationmemberships/v1alpha1",
			expectedUserID: "test-user",
			expectRewrite:  true,
			expectedPath:   "/apis/resourcemanager.miloapis.com/organizationmemberships/v1alpha1",
		},
		{
			name:           "valid user path direct user resource",
			requestPath:    "/apis/iam.miloapis.com/v1alpha1/users/test-user",
			expectedUserID: "test-user",
			expectRewrite:  false,
			expectedPath:   "/apis/iam.miloapis.com/v1alpha1/users/test-user",
		},
		{
			name:           "valid user path status subresource",
			requestPath:    "/apis/iam.miloapis.com/v1alpha1/users/test-user/status",
			expectedUserID: "test-user",
			expectRewrite:  false,
			expectedPath:   "/apis/iam.miloapis.com/v1alpha1/users/test-user/status",
		},
		{
			name:        "invalid user ID",
			requestPath: "/apis/iam.miloapis.com/v1alpha1/users/invalid@user/control-plane/apis/resourcemanager.miloapis.com/organizationmemberships/v1alpha1",
			expectError: true,
		},
		{
			name:         "non-user path",
			requestPath:  "/apis/resourcemanager.miloapis.com/organizationmemberships/v1alpha1",
			expectedPath: "/apis/resourcemanager.miloapis.com/organizationmemberships/v1alpha1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedUserID string
			var capturedPath string

			handler := UserContextHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if userID, ok := req.Context().Value(UserIDContextKey).(string); ok {
					capturedUserID = userID
				}
				capturedPath = req.URL.Path
			}), codecs)

			req := httptest.NewRequest("GET", "http://localhost"+tc.requestPath, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tc.expectError {
				if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
					t.Fatalf("expected error status, got %d", w.Code)
				}
				return
			}

			if tc.expectedUserID != "" && capturedUserID != tc.expectedUserID {
				t.Fatalf("expected user ID %q, got %q", tc.expectedUserID, capturedUserID)
			}

			if tc.expectedPath != "" && capturedPath != tc.expectedPath {
				t.Fatalf("expected path %q, got %q", tc.expectedPath, capturedPath)
			}
		})
	}
}

func TestUserContextAuthorizationDecorator(t *testing.T) {
	testCases := []struct {
		name           string
		userID         string
		expectUserInfo bool
	}{
		{
			name:           "with user ID in context",
			userID:         "test-user",
			expectUserInfo: true,
		},
		{
			name:           "without user ID in context",
			expectUserInfo: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedUser user.Info

			handler := UserContextAuthorizationDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if u, ok := request.UserFrom(req.Context()); ok {
					capturedUser = u
				}
			}))

			req := httptest.NewRequest("GET", "http://localhost/apis/iam.miloapis.com/v1alpha1/users", nil)
			ctx := req.Context()

			if tc.userID != "" {
				ctx = request.WithUser(ctx, &user.DefaultInfo{Name: "authenticated-user"})
				ctx = request.WithValue(ctx, UserIDContextKey, tc.userID)
			} else {
				ctx = request.WithUser(ctx, &user.DefaultInfo{Name: "authenticated-user"})
			}

			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tc.expectUserInfo {
				if capturedUser == nil {
					t.Fatal("expected user info in context")
				}

				extra := capturedUser.GetExtra()
				if extra == nil {
					t.Fatal("expected extra info on user")
				}

				if extra[iamv1alpha1.ParentAPIGroupExtraKey][0] != iamv1alpha1.SchemeGroupVersion.Group {
					t.Fatalf("expected parent API group %q, got %q", iamv1alpha1.SchemeGroupVersion.Group, extra[iamv1alpha1.ParentAPIGroupExtraKey][0])
				}

				if extra[iamv1alpha1.ParentKindExtraKey][0] != "User" {
					t.Fatalf("expected parent kind %q, got %q", "User", extra[iamv1alpha1.ParentKindExtraKey][0])
				}

				if extra[iamv1alpha1.ParentNameExtraKey][0] != tc.userID {
					t.Fatalf("expected parent name %q, got %q", tc.userID, extra[iamv1alpha1.ParentNameExtraKey][0])
				}
			}
		})
	}
}

func TestUserOrganizationMembershipListConstraintDecorator(t *testing.T) {
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
			name:                  "organizationmemberships list with user context",
			requestPath:           "/apis/resourcemanager.miloapis.com/v1alpha1/organizationmemberships",
			apiGroup:              resourcemanagerv1alpha1.GroupVersion.Group,
			resource:              "organizationmemberships",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "",
			expectedFieldSelector: ",spec.userRef.name=test-user",
		},
		{
			name:                  "organizationmemberships list with existing field selector",
			requestPath:           "/apis/resourcemanager.miloapis.com/v1alpha1/organizationmemberships",
			apiGroup:              resourcemanagerv1alpha1.GroupVersion.Group,
			resource:              "organizationmemberships",
			verb:                  "list",
			userID:                "test-user",
			existingFieldSelector: "spec.organizationRef.name=test-org",
			expectedFieldSelector: "spec.organizationRef.name=test-org,spec.userRef.name=test-user",
		},
		{
			name:        "non-organizationmemberships request",
			requestPath: "/api/v1/pods",
			apiGroup:    "",
			resource:    "pods",
			verb:        "list",
			userID:      "test-user",
		},
		{
			name:        "organizationmemberships get request",
			requestPath: "/apis/resourcemanager.miloapis.com/v1alpha1/organizationmemberships/test-membership",
			apiGroup:    resourcemanagerv1alpha1.GroupVersion.Group,
			resource:    "organizationmemberships",
			verb:        "get",
			userID:      "test-user",
		},
	}

	// A watch must be constrained exactly like a list, or it streams every
	// user's objects. Re-run each list case as a watch.
	for _, tc := range testCases {
		if tc.verb == "list" && tc.resource == "organizationmemberships" {
			tc.name += " (watch)"
			tc.verb = "watch"
			testCases = append(testCases, tc)
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedFieldSelector string

			handler := UserOrganizationMembershipListConstraintDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
