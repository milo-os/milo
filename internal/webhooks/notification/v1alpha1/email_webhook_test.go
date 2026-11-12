package v1alpha1

import (
	"context"
	"maps"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
)

var emailRuntimeScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(iamv1alpha1.AddToScheme(emailRuntimeScheme))
	utilruntime.Must(notificationv1alpha1.AddToScheme(emailRuntimeScheme))
}

func TestEmailValidator_ValidateCreate(t *testing.T) {
	// Common objects
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "john"},
		Spec:       iamv1alpha1.UserSpec{Email: "[email protected]"},
	}

	tmpl := &notificationv1alpha1.EmailTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "welcome"},
		Spec: notificationv1alpha1.EmailTemplateSpec{
			Subject:  "Welcome",
			HTMLBody: "<p>Hello {{.FirstName}}</p>",
			TextBody: "Hello {{.FirstName}}",
			Variables: []notificationv1alpha1.TemplateVariable{
				{Name: "FirstName", Required: true, Type: notificationv1alpha1.EmailTemplateVariableTypeString},
			},
		},
	}

	validEmail := &notificationv1alpha1.Email{
		ObjectMeta: metav1.ObjectMeta{Name: "welcome-john"},
		Spec: notificationv1alpha1.EmailSpec{
			TemplateRef: notificationv1alpha1.TemplateReference{Name: tmpl.Name},
			Recipient: notificationv1alpha1.EmailRecipient{
				UserRef: notificationv1alpha1.EmailUserReference{Name: user.Name},
			},
			Variables: []notificationv1alpha1.EmailVariable{
				{Name: "FirstName", Value: "John"},
			},
		},
	}

	tests := map[string]struct {
		includeUser   bool
		includeTmpl   bool
		email         *notificationv1alpha1.Email
		expectErr     bool
		errorContains string
	}{
		"valid with userRef": {
			includeUser: true, includeTmpl: true, email: validEmail, expectErr: false,
		},
		"valid with emailAddress": {
			includeUser: false, includeTmpl: true, email: func() *notificationv1alpha1.Email {
				e := validEmail.DeepCopy()
				e.Spec.Recipient.UserRef = notificationv1alpha1.EmailUserReference{}
				e.Spec.Recipient.EmailAddress = "john@example.com"
				return e
			}(), expectErr: false,
		},
		"both userRef and emailAddress": {
			includeUser: true, includeTmpl: true, email: func() *notificationv1alpha1.Email {
				e := validEmail.DeepCopy()
				e.Spec.Recipient.EmailAddress = "john@example.com"
				return e
			}(), expectErr: true, errorContains: "exactly one of emailAddress or userRef",
		},
		"neither userRef nor emailAddress": {
			includeUser: false, includeTmpl: true, email: func() *notificationv1alpha1.Email {
				e := validEmail.DeepCopy()
				e.Spec.Recipient.UserRef = notificationv1alpha1.EmailUserReference{}
				return e
			}(), expectErr: true, errorContains: "exactly one of emailAddress or userRef",
		},
		"invalid email format": {
			includeUser: false, includeTmpl: true, email: func() *notificationv1alpha1.Email {
				e := validEmail.DeepCopy()
				e.Spec.Recipient.UserRef = notificationv1alpha1.EmailUserReference{}
				e.Spec.Recipient.EmailAddress = "not-an-email"
				return e
			}(), expectErr: true, errorContains: "invalid email address",
		},
		"missing user": {
			includeUser: false, includeTmpl: true, email: validEmail, expectErr: true, errorContains: "userRef",
		},
		"missing template": {
			includeUser: true, includeTmpl: false, email: validEmail, expectErr: true, errorContains: "templateRef",
		},
		"variable error": {
			includeUser: true, includeTmpl: true, email: func() *notificationv1alpha1.Email {
				e := validEmail.DeepCopy()
				e.Spec.Variables = []notificationv1alpha1.EmailVariable{ // missing required FirstName
					{Name: "Extra", Value: "foo"},
				}
				return e
			}(), expectErr: true, errorContains: "required variable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(emailRuntimeScheme)
			if tt.includeUser {
				builder = builder.WithObjects(user)
			}
			if tt.includeTmpl {
				builder = builder.WithObjects(tmpl)
			}
			fakeClient := builder.Build()

			validator := &EmailValidator{Client: fakeClient}
			_, err := validator.ValidateCreate(context.Background(), tt.email)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Fatalf("error message %q does not contain %q", err.Error(), tt.errorContains)
				}
				if !apierrors.IsInvalid(err) {
					t.Fatalf("expected invalid error, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestEmailValidator_ValidateUpdateDelete(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(emailRuntimeScheme).Build()
	validator := &EmailValidator{Client: fakeClient}

	email := &notificationv1alpha1.Email{ObjectMeta: metav1.ObjectMeta{Name: "sample"}}

	// Update should be rejected
	_, err := validator.ValidateUpdate(context.Background(), email, email)
	if err == nil || !apierrors.IsMethodNotSupported(err) {
		t.Fatalf("expected MethodNotSupported error on update, got %v", err)
	}
}

func TestEmailMutator_Default(t *testing.T) {
	// 23:30 in New York is already the next day in UTC.
	newYork := time.FixedZone("EST", -5*60*60)
	clock := func() time.Time { return time.Date(2026, 11, 1, 23, 30, 0, 0, newYork) }

	tests := map[string]struct {
		labels map[string]string
		want   map[string]string
	}{
		"no labels": {
			labels: nil,
			want:   map[string]string{notificationv1alpha1.CreatedDateLabel: "2026-11-02"},
		},
		"keeps other labels": {
			labels: map[string]string{notificationv1alpha1.NotificationKindLabel: "x"},
			want: map[string]string{
				notificationv1alpha1.NotificationKindLabel: "x",
				notificationv1alpha1.CreatedDateLabel:      "2026-11-02",
			},
		},
		"replaces a client-supplied date": {
			labels: map[string]string{notificationv1alpha1.CreatedDateLabel: "1999-01-01"},
			want:   map[string]string{notificationv1alpha1.CreatedDateLabel: "2026-11-02"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			email := &notificationv1alpha1.Email{ObjectMeta: metav1.ObjectMeta{Name: "sample", Labels: tt.labels}}

			if err := (&EmailMutator{Now: clock}).Default(context.Background(), email); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !maps.Equal(email.Labels, tt.want) {
				t.Fatalf("labels = %v, want %v", email.Labels, tt.want)
			}
		})
	}
}

func TestEmailValidator_ValidateUpdate_CreatedDateBackfill(t *testing.T) {
	validator := &EmailValidator{Client: fake.NewClientBuilder().WithScheme(emailRuntimeScheme).Build()}

	stored := &notificationv1alpha1.Email{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sample",
			Namespace:         "milo-system",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 10, 28, 14, 38, 59, 0, time.UTC)),
			Labels:            map[string]string{notificationv1alpha1.NotificationKindLabel: "x"},
		},
		Spec: notificationv1alpha1.EmailSpec{Priority: "normal"},
	}
	withLabel := func(email *notificationv1alpha1.Email, key, value string) *notificationv1alpha1.Email {
		updated := email.DeepCopy()
		updated.Labels[key] = value
		return updated
	}
	backfilled := withLabel(stored, notificationv1alpha1.CreatedDateLabel, "2026-10-28")

	tests := map[string]struct {
		oldObj  *notificationv1alpha1.Email
		newObj  *notificationv1alpha1.Email
		allowed bool
	}{
		"adds the creation date": {
			oldObj:  stored,
			newObj:  backfilled,
			allowed: true,
		},
		"adds a different date": {
			oldObj: stored,
			newObj: withLabel(stored, notificationv1alpha1.CreatedDateLabel, "2026-10-29"),
		},
		"changes an existing date": {
			oldObj: withLabel(stored, notificationv1alpha1.CreatedDateLabel, "2026-10-27"),
			newObj: backfilled,
		},
		"also changes another label": {
			oldObj: stored,
			newObj: withLabel(backfilled, notificationv1alpha1.NotificationKindLabel, "y"),
		},
		"also changes annotations": {
			oldObj: stored,
			newObj: func() *notificationv1alpha1.Email {
				updated := backfilled.DeepCopy()
				updated.Annotations = map[string]string{"a": "b"}
				return updated
			}(),
		},
		"also changes the spec": {
			oldObj: stored,
			newObj: func() *notificationv1alpha1.Email {
				updated := backfilled.DeepCopy()
				updated.Spec.Priority = "high"
				return updated
			}(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := validator.ValidateUpdate(context.Background(), tt.oldObj, tt.newObj)
			if tt.allowed && err != nil {
				t.Fatalf("expected update to be allowed, got %v", err)
			}
			if !tt.allowed && !apierrors.IsMethodNotSupported(err) {
				t.Fatalf("expected MethodNotSupported error, got %v", err)
			}
		})
	}
}

// helper contains substring (avoid strings import collision)
func contains(s, substr string) bool { return strings.Contains(s, substr) }
