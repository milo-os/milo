// SPDX-License-Identifier: AGPL-3.0-only
package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// validateAssignableRole validates that the Role referenced by (name, namespace)
// lives in the assignable-roles catalog and actually exists there. It returns
// field errors ready to append to a validator's ErrorList, so it is reusable
// across webhook validators (a PolicyBinding's roleRef, a UserInvitation's
// roles, and any future validator that grants an assignable role).
//
// refPath must point at the object containing the role reference's Name and
// Namespace fields (e.g. spec.roleRef); the Name and Namespace errors are
// reported beneath it.
func validateAssignableRole(ctx context.Context, c client.Client, name, namespace, assignableRolesNamespace string, refPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	if namespace != assignableRolesNamespace {
		errs = append(errs, field.NotSupported(
			refPath.Child("namespace"),
			namespace,
			[]string{assignableRolesNamespace},
		))
	} else {
		role := &iamv1alpha1.Role{}
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, role); err != nil {
			if errors.IsNotFound(err) {
				errs = append(errs, field.NotFound(
					refPath.Child("name"),
					fmt.Sprintf("%s/%s", namespace, name),
				))
			} else {
				errs = append(errs, field.InternalError(
					refPath.Child("name"),
					fmt.Errorf("failed to get role %q: %w", name, err),
				))
			}
		}
	}

	return errs
}
