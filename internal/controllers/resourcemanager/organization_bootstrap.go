package resourcemanager

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// EnsureOrganizationNamespace creates the organization-scoped namespace if it does not exist.
func EnsureOrganizationNamespace(ctx context.Context, c client.Client, org *resourcemanagerv1alpha.Organization) error {
	namespaceName := resourcemanagerv1alpha.OrganizationNamespace(org.Name)

	var existing corev1.Namespace
	if err := c.Get(ctx, types.NamespacedName{Name: namespaceName}, &existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting organization namespace: %w", err)
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
			Labels: map[string]string{
				"resourcemanager.miloapis.com/organization": org.Name,
				"resourcemanager.miloapis.com/type":         "organization",
			},
		},
	}

	if err := c.Create(ctx, namespace); err != nil {
		return fmt.Errorf("creating organization namespace: %w", err)
	}

	return nil
}

// EnsureOwnerOrganizationMembership creates the owner OrganizationMembership for the creator if missing.
func EnsureOwnerOrganizationMembership(
	ctx context.Context,
	c client.Client,
	org *resourcemanagerv1alpha.Organization,
	user *iamv1alpha1.User,
	ownerRoleName string,
	ownerRoleNamespace string,
) error {
	membershipName := fmt.Sprintf("member-%s", user.Name)
	namespaceName := resourcemanagerv1alpha.OrganizationNamespace(org.Name)

	var existing resourcemanagerv1alpha.OrganizationMembership
	if err := c.Get(ctx, types.NamespacedName{Name: membershipName, Namespace: namespaceName}, &existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting owner organization membership: %w", err)
	}

	organizationMembership := &resourcemanagerv1alpha.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      membershipName,
			Namespace: namespaceName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: iamv1alpha1.SchemeGroupVersion.String(),
					Kind:       "User",
					Name:       user.Name,
					UID:        user.UID,
				},
			},
		},
		Spec: resourcemanagerv1alpha.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha.OrganizationReference{
				Name: org.Name,
			},
			UserRef: resourcemanagerv1alpha.MemberReference{
				Name: user.Name,
			},
			Roles: []resourcemanagerv1alpha.RoleReference{
				{
					Name:      ownerRoleName,
					Namespace: ownerRoleNamespace,
				},
			},
		},
	}

	if err := c.Create(ctx, organizationMembership); err != nil {
		return fmt.Errorf("creating owner organization membership: %w", err)
	}

	return nil
}

func (r *OrganizationController) reconcileOrganizationOwnerBootstrap(
	ctx context.Context,
	organization *resourcemanagerv1alpha.Organization,
) error {
	creatorUID, ok := organization.Annotations[resourcemanagerv1alpha.OrganizationCreatorUserUIDAnnotation]
	if !ok || creatorUID == "" {
		return nil
	}

	if err := EnsureOrganizationNamespace(ctx, r.Client, organization); err != nil {
		return err
	}

	var user iamv1alpha1.User
	if err := r.Client.Get(ctx, types.NamespacedName{Name: creatorUID}, &user); err != nil {
		return fmt.Errorf("getting organization creator user %q: %w", creatorUID, err)
	}

	if err := EnsureOwnerOrganizationMembership(
		ctx,
		r.Client,
		organization,
		&user,
		r.OwnerRoleName,
		r.OwnerRoleNamespace,
	); err != nil {
		return err
	}

	patch := client.MergeFrom(organization.DeepCopy())
	delete(organization.Annotations, resourcemanagerv1alpha.OrganizationCreatorUserUIDAnnotation)
	if err := r.Client.Patch(ctx, organization, patch); err != nil {
		return fmt.Errorf("removing organization creator annotation: %w", err)
	}

	return nil
}
