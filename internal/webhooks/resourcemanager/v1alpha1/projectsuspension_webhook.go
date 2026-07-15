package v1alpha1

import (
	"context"
	"fmt"

	"go.miloapis.com/milo/pkg/features"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var projectsuspensionlog = logf.Log.WithName("projectsuspension-resource")

func SetupProjectSuspensionWebhooksWithManager(mgr ctrl.Manager, systemNamespace string) error {
	projectsuspensionlog.Info("Setting up resourcemanager.miloapis.com projectsuspension webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &resourcemanagerv1alpha1.ProjectSuspension{}).
		WithDefaulter(&ProjectSuspensionMutator{client: mgr.GetClient(), scheme: mgr.GetScheme()}).
		WithValidator(&ProjectSuspensionValidator{
			client:          mgr.GetClient(),
			systemNamespace: systemNamespace,
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-resourcemanager-miloapis-com-v1alpha1-projectsuspension,mutating=true,failurePolicy=fail,sideEffects=None,groups=resourcemanager.miloapis.com,resources=projectsuspensions,verbs=create,versions=v1alpha1,name=mprojectsuspension.resourcemanager.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ProjectSuspensionMutator struct {
	client client.Client
	scheme *runtime.Scheme
}

func (m *ProjectSuspensionMutator) Default(ctx context.Context, ps *resourcemanagerv1alpha1.ProjectSuspension) error {
	projectsuspensionlog.Info("Defaulting ProjectSuspension", "name", ps.GetName())

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		projectsuspensionlog.Error(err, "failed to get admission request from context", "name", ps.GetName())
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	if ps.Spec.RequestedBy == "" {
		ps.Spec.RequestedBy = req.UserInfo.Username
	}

	// Set owner reference so ProjectSuspension is garbage collected when Project is deleted.
	project := &resourcemanagerv1alpha1.Project{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ps.Spec.ProjectRef.Name}, project); err != nil {
		projectsuspensionlog.Error(err, "failed to fetch referenced Project while setting owner reference", "projectName", ps.Spec.ProjectRef.Name)
		return errors.NewInternalError(fmt.Errorf("failed to fetch referenced Project while setting owner reference: %w", err))
	}
	if err := controllerutil.SetOwnerReference(project, ps, m.scheme); err != nil {
		return errors.NewInternalError(fmt.Errorf("failed to set owner reference for project deactivation: %w", err))
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-resourcemanager-miloapis-com-v1alpha1-projectsuspension,mutating=false,failurePolicy=fail,sideEffects=None,groups=resourcemanager.miloapis.com,resources=projectsuspensions,verbs=create;update;delete,versions=v1alpha1,name=vprojectsuspension.resourcemanager.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ProjectSuspensionValidator struct {
	client          client.Client
	systemNamespace string
}

func (v *ProjectSuspensionValidator) ValidateCreate(ctx context.Context, ps *resourcemanagerv1alpha1.ProjectSuspension) (admission.Warnings, error) {
	projectsuspensionlog.Info("Validating ProjectSuspension", "name", ps.Name)

	if !utilfeature.DefaultFeatureGate.Enabled(features.ProjectSuspension) {
		return nil, errors.NewForbidden(
			resourcemanagerv1alpha1.GroupVersion.WithResource("projectsuspensions").GroupResource(),
			ps.Name,
			fmt.Errorf("project suspension feature gate is not enabled"),
		)
	}

	var errs field.ErrorList

	projectName := ps.Spec.ProjectRef.Name

	// Validate that the referenced project exists
	project := &resourcemanagerv1alpha1.Project{}
	err := v.client.Get(ctx, client.ObjectKey{Name: projectName}, project)
	if errors.IsNotFound(err) {
		projectsuspensionlog.Error(err, "referenced project does not exist", "projectName", projectName)
		errs = append(errs, field.NotFound(field.NewPath("spec").Child("projectRef").Child("name"), projectName))
	} else if err != nil {
		projectsuspensionlog.Error(err, "failed to validate project reference", "projectName", projectName)
		return nil, errors.NewInternalError(fmt.Errorf("failed to validate project reference"))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(resourcemanagerv1alpha1.GroupVersion.WithKind("ProjectSuspension").GroupKind(), ps.Name, errs)
	}

	return nil, nil
}

func (v *ProjectSuspensionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *resourcemanagerv1alpha1.ProjectSuspension) (admission.Warnings, error) {
	return nil, nil
}

func (v *ProjectSuspensionValidator) ValidateDelete(ctx context.Context, obj *resourcemanagerv1alpha1.ProjectSuspension) (admission.Warnings, error) {
	return nil, nil
}
