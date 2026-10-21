package resourcemanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"go.miloapis.com/milo/internal/controllers/projectpurge"
)

type recordingDeleter struct {
	calls   [][]string
	refused map[string]error
	err     error
}

func (d *recordingDeleter) DeleteNamespaces(_ context.Context, _ *rest.Config, names []string) (map[string]error, error) {
	d.calls = append(d.calls, names)
	return d.refused, d.err
}

func TestDeleteLateNamespaces_DeletesOnlyTheUnmarkedNamespaces(t *testing.T) {
	cleanup := projectpurge.Status{Blockers: []projectpurge.Blocker{
		{Namespace: "late"},
		{Namespace: "demo", Marked: true},
		{Namespace: "default", Resource: "configmaps", Name: "held"},
	}}
	d := &recordingDeleter{}

	if err := deleteLateNamespaces(context.Background(), d, &rest.Config{}, &cleanup); err != nil {
		t.Fatalf("deleteLateNamespaces: %v", err)
	}
	if len(d.calls) != 1 || len(d.calls[0]) != 1 || d.calls[0][0] != "late" {
		t.Fatalf("expected one delete call for late, got %v", d.calls)
	}
}

func TestDeleteLateNamespaces_CallsNothingWhenEveryNamespaceIsMarked(t *testing.T) {
	cleanup := projectpurge.Status{Blockers: []projectpurge.Blocker{{Namespace: "demo", Marked: true}}}
	d := &recordingDeleter{}

	if err := deleteLateNamespaces(context.Background(), d, &rest.Config{}, &cleanup); err != nil {
		t.Fatalf("deleteLateNamespaces: %v", err)
	}
	if len(d.calls) != 0 {
		t.Fatalf("expected no delete call, got %v", d.calls)
	}
}

func TestDeleteLateNamespaces_TheStatusNamesARefusal(t *testing.T) {
	cleanup := projectpurge.Status{Blockers: []projectpurge.Blocker{{Namespace: "reserved"}}}
	d := &recordingDeleter{refused: map[string]error{"reserved": errors.New("held by a reservation")}}

	if err := deleteLateNamespaces(context.Background(), d, &rest.Config{}, &cleanup); err != nil {
		t.Fatalf("deleteLateNamespaces: %v", err)
	}
	if msg := cleanup.Message(); !strings.Contains(msg, "held by a reservation") {
		t.Fatalf("expected the status to name the refusal, got %q", msg)
	}
}

func TestDeleteLateNamespaces_ReturnsAClientError(t *testing.T) {
	cleanup := projectpurge.Status{Blockers: []projectpurge.Blocker{{Namespace: "late"}}}
	d := &recordingDeleter{err: errors.New("typed core: bad config")}

	if err := deleteLateNamespaces(context.Background(), d, &rest.Config{}, &cleanup); err == nil {
		t.Fatal("expected the client error to be returned")
	}
}
