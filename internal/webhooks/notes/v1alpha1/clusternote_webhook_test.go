package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notesv1alpha1 "go.miloapis.com/milo/pkg/apis/notes/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Note: newTestRESTMapper is defined in note_webhook_test.go and shared across tests in this package

func TestClusterNoteMutator_Default(t *testing.T) {
	tests := map[string]struct {
		clusterNote   *notesv1alpha1.ClusterNote
		user          *iamv1alpha1.User
		subject       *unstructured.Unstructured
		expectError   bool
		errorContains string
	}{
		"successful owner reference setup": {
			clusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test cluster note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "resourcemanager.miloapis.com",
						Kind:      "Organization",
						Name:      "test-org",
						Namespace: "", // cluster-scoped
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
					"apiVersion": "resourcemanager.miloapis.com/v1alpha1",
					"kind":       "Organization",
					"metadata": map[string]interface{}{
						"name": "test-org",
						"uid":  "org-uid-123",
					},
				},
			},
			expectError: false,
		},
		"subject not found": {
			clusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test cluster note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "resourcemanager.miloapis.com",
						Kind:      "Organization",
						Name:      "nonexistent-org",
						Namespace: "",
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
		"subject is namespaced (should not set owner ref)": {
			clusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test cluster note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "some-namespace",
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
			mutator := &ClusterNoteMutator{
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

			err := mutator.Default(ctx, tc.clusterNote)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.user.Name, tc.clusterNote.Spec.CreatorRef.Name)

				// Verify owner reference if subject is cluster-scoped
				if tc.subject != nil && tc.clusterNote.Spec.SubjectRef.Namespace == "" {
					require.Len(t, tc.clusterNote.OwnerReferences, 1)
					assert.Equal(t, tc.subject.GetKind(), tc.clusterNote.OwnerReferences[0].Kind)
					assert.Equal(t, tc.subject.GetName(), tc.clusterNote.OwnerReferences[0].Name)
				}
			}
		})
	}
}

func TestClusterNoteValidator_ValidateCreate(t *testing.T) {
	tests := map[string]struct {
		clusterNote   *notesv1alpha1.ClusterNote
		expectError   bool
		errorContains string
	}{
		"valid cluster note with cluster-scoped subject": {
			clusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test cluster note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "resourcemanager.miloapis.com",
						Kind:      "Organization",
						Name:      "test-org",
						Namespace: "", // cluster-scoped
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError: false,
		},
		"invalid: subject has namespace": {
			clusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Test cluster note content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "some-namespace", // namespaced - invalid for ClusterNote
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError:   true,
			errorContains: "ClusterNote can only reference cluster-scoped resources",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
			validator := &ClusterNoteValidator{Client: fakeClient}

			_, err := validator.ValidateCreate(context.Background(), tc.clusterNote)

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

func TestClusterNoteValidator_ValidateUpdate(t *testing.T) {
	oldClusterNote := &notesv1alpha1.ClusterNote{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-clusternote",
		},
		Spec: notesv1alpha1.NoteSpec{
			Content: "Old content",
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
	}

	tests := map[string]struct {
		newClusterNote *notesv1alpha1.ClusterNote
		expectError    bool
		errorContains  string
	}{
		"valid update": {
			newClusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
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
			expectError: false,
		},
		"invalid: changing to namespaced subject": {
			newClusterNote: &notesv1alpha1.ClusterNote{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusternote",
				},
				Spec: notesv1alpha1.NoteSpec{
					Content: "Updated content",
					SubjectRef: notesv1alpha1.SubjectReference{
						APIGroup:  "networking.datumapis.com",
						Kind:      "Domain",
						Name:      "test-domain",
						Namespace: "some-namespace",
					},
					CreatorRef: iamv1alpha1.UserReference{
						Name: "test-user",
					},
				},
			},
			expectError:   true,
			errorContains: "ClusterNote can only reference cluster-scoped resources",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
			validator := &ClusterNoteValidator{Client: fakeClient}

			_, err := validator.ValidateUpdate(context.Background(), oldClusterNote, tc.newClusterNote)

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
