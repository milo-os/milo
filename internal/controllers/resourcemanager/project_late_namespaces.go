package resourcemanager

import (
	"context"

	"k8s.io/client-go/rest"

	"go.miloapis.com/milo/internal/controllers/projectpurge"
)

// namespaceDeleter deletes named namespaces in a project's control plane.
// projectpurge.Purger is the one in use.
type namespaceDeleter interface {
	DeleteNamespaces(ctx context.Context, cfg *rest.Config, names []string) (map[string]error, error)
}

// deleteLateNamespaces deletes the namespaces that appeared in a deleting
// project's control plane after the purge's sweep, and records each refused
// delete on its blocker, so the project's status names it.
func deleteLateNamespaces(ctx context.Context, d namespaceDeleter, cfg *rest.Config, cleanup *projectpurge.Status) error {
	return nil
}
