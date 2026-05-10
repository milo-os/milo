package useridentities

import (
	"context"
	"time"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

type Backend interface {
	ListUserIdentities(ctx context.Context, u authuser.Info, opts *metav1.ListOptions) (*identityv1alpha1.UserIdentityList, error)
	GetUserIdentity(ctx context.Context, u authuser.Info, name string) (*identityv1alpha1.UserIdentity, error)
}

type REST struct {
	backend Backend
}

var _ rest.Scoper = &REST{}
var _ rest.Lister = &REST{}
var _ rest.Getter = &REST{}
var _ rest.Storage = &REST{}
var _ rest.SingularNameProvider = &REST{}

func NewREST(b Backend) *REST { return &REST{backend: b} }

func (r *REST) GetSingularName() string { return "useridentity" }
func (r *REST) NamespaceScoped() bool   { return false }
func (r *REST) New() runtime.Object     { return &identityv1alpha1.UserIdentity{} }
func (r *REST) NewList() runtime.Object { return &identityv1alpha1.UserIdentityList{} }

func (r *REST) List(ctx context.Context, opts *metainternalversion.ListOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)
	u, _ := apirequest.UserFrom(ctx)
	username, uid, groups := "", "", []string(nil)
	if u != nil {
		username = u.GetName()
		uid = u.GetUID()
		groups = u.GetGroups()
	}
	// Forward the caller's selectors to the backend so cross-user lookups
	// (e.g. status.userUID=<uid>) reach the auth-provider-zitadel REST
	// handler. The handler runs its own SAR check against milo using the
	// caller identity preserved via X-Remote-* headers in DynamicProvider.
	lo := metav1.ListOptions{}
	if opts != nil {
		if opts.FieldSelector != nil {
			lo.FieldSelector = opts.FieldSelector.String()
		}
		if opts.LabelSelector != nil {
			lo.LabelSelector = opts.LabelSelector.String()
		}
	}
	logger.V(4).Info("Listing user identities", "username", username, "uid", uid, "groups", groups, "fieldSelector", lo.FieldSelector, "labelSelector", lo.LabelSelector)
	res, err := r.backend.ListUserIdentities(ctx, u, &lo)
	if err != nil {
		logger.Error(err, "List user identities failed")
		return nil, err
	}
	logger.V(4).Info("Listed user identities", "count", len(res.Items))
	return res, nil
}

func (r *REST) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)
	u, _ := apirequest.UserFrom(ctx)
	username, uid := "", ""
	if u != nil {
		username = u.GetName()
		uid = u.GetUID()
	}
	logger.V(4).Info("Getting user identity", "name", name, "username", username, "uid", uid)
	res, err := r.backend.GetUserIdentity(ctx, u, name)
	if err != nil {
		logger.Error(err, "Get user identity failed", "name", name)
		return nil, err
	}
	logger.V(4).Info("Got user identity", "name", name, "provider", res.Status.ProviderName, "userUID", res.Status.UserUID)
	return res, nil
}

func (r *REST) Destroy() {}

// Satisfy rest.TableConvertor with a no-op conversion (returning nil uses default table printer)
func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Provider", Type: "string"},
			{Name: "Username", Type: "string"},
			{Name: "Age", Type: "date"},
		},
	}

	appendRow := func(ui *identityv1alpha1.UserIdentity) {
		age := metav1.Now().Rfc3339Copy()
		if !ui.CreationTimestamp.IsZero() {
			// age shown as since creation, consistent with kubectl
			// metav1.Table wants a date in the cell; pass the timestamp
			age = ui.CreationTimestamp
		}
		table.Rows = append(table.Rows, metav1.TableRow{
			Cells:  []interface{}{ui.Name, ui.Status.ProviderName, ui.Status.Username, age.Time.Format(time.RFC3339)},
			Object: runtime.RawExtension{Object: ui},
		})
	}

	switch obj := object.(type) {
	case *identityv1alpha1.UserIdentityList:
		for i := range obj.Items {
			ui := &obj.Items[i]
			appendRow(ui)
		}
	case *identityv1alpha1.UserIdentity:
		appendRow(obj)
	default:
		// Fallback to default printer
		return nil, nil
	}

	return table, nil
}
