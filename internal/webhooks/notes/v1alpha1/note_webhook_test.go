package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notesv1alpha1 "go.miloapis.com/milo/pkg/apis/notes/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var testScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(iamv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(notesv1alpha1.AddToScheme(testScheme))
}

// newTestRESTMapper creates a RESTMapper for testing that knows about common test resources
func newTestRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "networking.datumapis.com", Version: "v1alpha1"},
		{Group: "resourcemanager.miloapis.com", Version: "v1alpha1"},
	})
	// Register Domain as a namespaced resource
	mapper.Add(schema.GroupVersionKind{
		Group:   "networking.datumapis.com",
		Version: "v1alpha1",
		Kind:    "Domain",
	}, meta.RESTScopeNamespace)
	// Register Organization as a cluster-scoped resource
	mapper.Add(schema.GroupVersionKind{
		Group:   "resourcemanager.miloapis.com",
		Version: "v1alpha1",
		Kind:    "Organization",
	}, meta.RESTScopeRoot)
	return mapper
}

func TestNoteMutator_Default(t *testing.T) {
	tests := map[string]struct {
		note          *notesv1alpha1.Note
		user          *iamv1alpha1.User
		subject       *unstructured.Unstructured
		expectError   bool
		errorContains string
	}{
		"successful owner reference setup": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "test-namespace",
					},
				},
			},
			user: &iamv1alpha1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-user-uid",
					UID:  types.UID("test-user-uid"),
				},
				Spec: iamv1alpha1.UserSpec{
					Email: "test@example.com",
				},
			},
			subject: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "networking.datumapis.com/v1alpha1",
					"kind":       "Domain",
					"metadata": map[string]interface{}{
						"name":      "test-domain",
						"namespace": "test-namespace",
						"uid":       "domain-uid-123",
					},
				},
			},
			expectError: false,
		},
		"subject not found": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "nonexistent-domain",
						Namespace: "test-namespace",
					},
				},
			},
			user: &iamv1alpha1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-user-uid",
					UID:  types.UID("test-user-uid"),
				},
				Spec: iamv1alpha1.UserSpec{
					Email: "test@example.com",
				},
			},
			expectError:   true,
			errorContains: "subject resource not found",
		},
		"subject in different namespace": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "other-namespace",
					},
				},
			},
			user: &iamv1alpha1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-user-uid",
					UID:  types.UID("test-user-uid"),
				},
				Spec: iamv1alpha1.UserSpec{
					Email: "test@example.com",
				},
			},
			expectError: false, // Should not set owner reference but not fail
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			objects := []client.Object{tc.user}
			if tc.subject != nil {
				objects = append(objects, tc.subject)
			}

			fakeClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objects...).Build()
			mutator := &NoteMutator{
				Client:         fakeClient,
				Scheme:         testScheme,
				RESTMapper:     newTestRESTMapper(),
				ClusterManager: nil, // nil means local-only search (backwards compatible)
			}

			// Create admission request context
			ctx := context.Background()
			req := admission.Request{}
			req.UserInfo.UID = "test-user-uid"
			ctx = admission.NewContextWithRequest(ctx, req)

			err := mutator.Default(ctx, tc.note)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.user.Name, tc.note.Spec.CreatorRef.Name)

				// Verify owner reference if subject is in same namespace
				if tc.subject != nil && tc.note.Spec.SubjectRef.Namespace == tc.note.Namespace {
					require.Len(t, tc.note.OwnerReferences, 1)
					assert.Equal(t, tc.subject.GetKind(), tc.note.OwnerReferences[0].Kind)
					assert.Equal(t, tc.subject.GetName(), tc.note.OwnerReferences[0].Name)
				}
			}
		})
	}
}

func TestNoteValidator_ValidateCreate(t *testing.T) {
	tests := map[string]struct {
		note          *notesv1alpha1.Note
		expectError   bool
		errorContains string
	}{
		"valid note with namespaced subject in same namespace": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "test-namespace",
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError: false,
		},
		"invalid: subject namespace is empty": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "resourcemanager.miloapis.com",
						Kind:      "Organization",
						Name:      "test-org",
						Namespace: "", // cluster-scoped - invalid for Note
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError:   true,
			errorContains: "Note can only reference namespaced resources",
		},
		"invalid: subject in different namespace": {
			note: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "other-namespace",
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError:   true,
			errorContains: "Note must be in the same namespace as its subject",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
			validator := &NoteValidator{Client: fakeClient}

			_, err := validator.ValidateCreate(context.Background(), tc.note)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNoteValidator_ValidateUpdate(t *testing.T) {
	oldNote := &notesv1alpha1.Note{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-note",
			Namespace: "test-namespace",
		},
		Spec: notesv1alpha1.NoteSpec{
			Content: "Old content",
			SubjectRef: notesv1alpha1.SubjectReference{
				APIGroup:  "networking.datumapis.com",
				Kind:      "Domain",
				Name:      "test-domain",
				Namespace: "test-namespace",
			},
			CreatorRef: iamv1alpha1.UserReference{
				Name: "test-user",
			},
		},
	}

	tests := map[string]struct {
		newNote       *notesv1alpha1.Note
		expectError   bool
		errorContains string
	}{
		"valid update": {
			newNote: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Updated content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "test-namespace",
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError: false,
		},
		"invalid: changing to cluster-scoped subject": {
			newNote: &notesv1alpha1.Note{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-note",
					Namespace: "test-namespace",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Updated content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "resourcemanager.miloapis.com",
						Kind:      "Organization",
						Name:      "test-org",
						Namespace: "",
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError:   true,
			errorContains: "Note can only reference namespaced resources",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
			validator := &NoteValidator{Client: fakeClient}

			_, err := validator.ValidateUpdate(context.Background(), oldNote, tc.newNote)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
