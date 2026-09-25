package v1alpha1_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// TestUserInvitationIsExpired pins the expiration semantics shared by the
// validating webhook and the controller: an expiration date strictly in the
// past means expired, a future date does not, and a missing date never expires.
// IsoExpired computes time.Now() internally, so the assertions anchor clearly
// past/future dates rather than attempting a racy exactly-now check.
func TestUserInvitationIsExpired(t *testing.T) {
	now := time.Now().UTC()
	past := metav1.NewTime(now.Add(-1 * time.Hour))
	future := metav1.NewTime(now.Add(1 * time.Hour))

	tests := map[string]struct {
		expirationDate *metav1.Time
		expectExpired  bool
	}{
		"expiration in the past": {expirationDate: &past, expectExpired: true},
		"expiration in the future": {
			expirationDate: &future,
			expectExpired:  false,
		},
		"no expiration date never expires": {expirationDate: nil, expectExpired: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ui := &iamv1alpha1.UserInvitation{
				Spec: iamv1alpha1.UserInvitationSpec{
					ExpirationDate: tc.expirationDate,
				},
			}
			if got := ui.IsExpired(); got != tc.expectExpired {
				t.Errorf("IsExpired() = %v, want %v (expirationDate=%v)", got, tc.expectExpired, tc.expirationDate)
			}
		})
	}
}
