package passkeyregistrationlinks

import (
	"context"
	"time"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

// Backend is the interface that the REST handler delegates creates to.
// Implementations proxy requests to the auth-provider (e.g. Zitadel) service.
type Backend interface {
	CreatePasskeyRegistrationLink(ctx context.Context, u authuser.Info, link *identityv1alpha1.PasskeyRegistrationLink, opts *metav1.CreateOptions) (*identityv1alpha1.PasskeyRegistrationLink, error)
}

// REST is create-only: the provider cannot revoke or list issued links.
type REST struct {
	backend Backend
}

var _ rest.Scoper = &REST{}
var _ rest.Creater = &REST{} //nolint:misspell
var _ rest.Storage = &REST{}
var _ rest.SingularNameProvider = &REST{}

func NewREST(b Backend) *REST { return &REST{backend: b} }

func (r *REST) GetSingularName() string { return "passkeyregistrationlink" }
func (r *REST) NamespaceScoped() bool   { return false }
func (r *REST) New() runtime.Object     { return &identityv1alpha1.PasskeyRegistrationLink{} }

func (r *REST) Create(
	ctx context.Context,
	obj runtime.Object,
	_ rest.ValidateObjectFunc,
	opts *metav1.CreateOptions,
) (runtime.Object, error) {
	logger := klog.FromContext(ctx)
	u, _ := apirequest.UserFrom(ctx)
	link, ok := obj.(*identityv1alpha1.PasskeyRegistrationLink)
	if !ok {
		return nil, apierrors.NewBadRequest("not a PasskeyRegistrationLink")
	}
	logger.V(4).Info("Creating passkey registration link", "user", link.Spec.UserRef.Name, "requestedBy", link.Spec.RequestedBy)
	res, err := r.backend.CreatePasskeyRegistrationLink(ctx, u, link, opts)
	if err != nil {
		logger.Error(err, "Create passkey registration link failed", "user", link.Spec.UserRef.Name)
		return nil, err
	}
	logger.V(4).Info("Created passkey registration link", "name", res.Name, "user", link.Spec.UserRef.Name, "emailName", res.Status.EmailName)
	return res, nil
}

func (r *REST) Destroy() {}

// ConvertToTable satisfies rest.TableConvertor with a kubectl-friendly table output.
func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "User", Type: "string"},
			{Name: "Age", Type: "date"},
		},
	}

	link, ok := object.(*identityv1alpha1.PasskeyRegistrationLink)
	if !ok {
		return nil, nil
	}
	age := metav1.Now().Rfc3339Copy()
	if !link.CreationTimestamp.IsZero() {
		age = link.CreationTimestamp
	}
	table.Rows = append(table.Rows, metav1.TableRow{
		Cells:  []interface{}{link.Name, link.Spec.UserRef.Name, age.Time.Format(time.RFC3339)},
		Object: runtime.RawExtension{Object: link},
	})

	return table, nil
}
