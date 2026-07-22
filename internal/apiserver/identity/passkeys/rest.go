package passkeys

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
	ListPasskeys(ctx context.Context, u authuser.Info, opts *metav1.ListOptions) (*identityv1alpha1.PasskeyList, error)
	GetPasskey(ctx context.Context, u authuser.Info, name string) (*identityv1alpha1.Passkey, error)
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

func (r *REST) GetSingularName() string { return "passkey" }
func (r *REST) NamespaceScoped() bool   { return false }
func (r *REST) New() runtime.Object     { return &identityv1alpha1.Passkey{} }
func (r *REST) NewList() runtime.Object { return &identityv1alpha1.PasskeyList{} }

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
	// (status.userUID=<uid>, for staff support) reach the zitadel-provider
	// REST handler, which runs its own SAR check against milo using the
	// caller identity preserved via X-Remote-* headers in DynamicProvider.
	// Absent a selector, the backend scopes the list to the caller's own
	// passkeys via the X-Remote-Uid header.
	lo := metav1.ListOptions{}
	if opts != nil {
		if opts.FieldSelector != nil {
			lo.FieldSelector = opts.FieldSelector.String()
		}
		if opts.LabelSelector != nil {
			lo.LabelSelector = opts.LabelSelector.String()
		}
	}
	logger.V(4).Info("Listing passkeys", "username", username, "uid", uid, "groups", groups, "fieldSelector", lo.FieldSelector, "labelSelector", lo.LabelSelector)
	res, err := r.backend.ListPasskeys(ctx, u, &lo)
	if err != nil {
		logger.Error(err, "List passkeys failed")
		return nil, err
	}
	logger.V(4).Info("Listed passkeys", "count", len(res.Items))
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
	logger.V(4).Info("Getting passkey", "name", name, "username", username, "uid", uid)
	res, err := r.backend.GetPasskey(ctx, u, name)
	if err != nil {
		logger.Error(err, "Get passkey failed", "name", name)
		return nil, err
	}
	logger.V(4).Info("Got passkey", "name", name, "state", res.Status.State, "userUID", res.Status.UserUID)
	return res, nil
}

func (r *REST) Destroy() {}

func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Display Name", Type: "string"},
			{Name: "State", Type: "string"},
			{Name: "Age", Type: "date"},
		},
	}

	appendRow := func(p *identityv1alpha1.Passkey) {
		age := metav1.Now().Rfc3339Copy()
		if !p.CreationTimestamp.IsZero() {
			age = p.CreationTimestamp
		}
		table.Rows = append(table.Rows, metav1.TableRow{
			Cells:  []interface{}{p.Name, p.Status.DisplayName, string(p.Status.State), age.Time.Format(time.RFC3339)},
			Object: runtime.RawExtension{Object: p},
		})
	}

	switch obj := object.(type) {
	case *identityv1alpha1.PasskeyList:
		for i := range obj.Items {
			appendRow(&obj.Items[i])
		}
	case *identityv1alpha1.Passkey:
		appendRow(obj)
	default:
		return nil, nil
	}

	return table, nil
}
