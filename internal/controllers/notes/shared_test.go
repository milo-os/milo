package notes

import (
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notesv1alpha1 "go.miloapis.com/milo/pkg/apis/notes/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestCreatorResourceRef proves a creator's PolicyBinding names the note it grants: a Note with its namespace,
// without which the binding cannot be resolved, and a ClusterNote with none.
func TestCreatorResourceRef(t *testing.T) {
	for _, tc := range []struct {
		name string
		note NoteResource
		want iamv1alpha1.ResourceReference
	}{
		{name: "a Note names its namespace",
			note: &notesv1alpha1.Note{ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "team", UID: "note-uid"}},
			want: iamv1alpha1.ResourceReference{APIGroup: "notes.miloapis.com", Kind: "Note", Name: "n", UID: "note-uid",
				Namespace: "team"}},
		{name: "a ClusterNote names none",
			note: &notesv1alpha1.ClusterNote{ObjectMeta: metav1.ObjectMeta{Name: "cn", UID: "clusternote-uid"}},
			want: iamv1alpha1.ResourceReference{APIGroup: "notes.miloapis.com", Kind: "ClusterNote", Name: "cn",
				UID: "clusternote-uid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := creatorResourceRef(tc.note); *got != tc.want {
				t.Errorf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}
