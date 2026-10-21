package resourcemanager

import (
	"context"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
	late := cleanup.Unmarked()
	if len(late) == 0 {
		return nil
	}
	refused, err := d.DeleteNamespaces(ctx, cfg, late)
	if err != nil {
		return err
	}
	cleanup.NoteDeletes(late, refused)
	return nil
}

// clearLateNamespaces deletes a deleting project's late namespaces at this
// poll. A failure is logged, and the next poll tries again.
func (r *ProjectController) clearLateNamespaces(ctx context.Context, cfg *rest.Config, project string, cleanup *projectpurge.Status) {
	if err := deleteLateNamespaces(ctx, r.Purger, cfg, cleanup); err != nil {
		log.FromContext(ctx).Error(err, "delete namespaces that appeared after the purge", "project", project)
	}
}
