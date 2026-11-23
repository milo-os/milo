// Package accessowner implements the AccessRuleOwnerEnforcement admission
// plugin. It keeps the owner field of resources with an access rule (see
// go.miloapis.com/milo/pkg/server/access) honest on writes, which the access
// filter can't check because it runs before the request body is decoded.
//
// In the user context and in an organization context, where an owner rule
// limits what the caller sees:
//
//   - Create (including a server-side apply that creates) must set the owner
//     field to the request's owner: the user in the path, or the caller.
//   - Update and patch must not change the owner field.
//
// In every context outside projects, a write to a resource whose rule is
// invalid or not trusted yet is refused unless the caller is in
// system:masters, like the access filter does for every request.
//
// It also validates the access-rule annotations of CustomResourceDefinitions
// on create and update (see validateCRD), so an invalid declaration is
// refused when it is written rather than found when it is served.
//
// The plugin validates and doesn't mutate, so it runs after mutating
// webhooks: a service's webhook can still fill the owner field in from the
// request, and this plugin checks the result. The platform context, where
// staff and services work, isn't checked: services there create objects on
// a user's behalf.
package accessowner

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"

	milorequest "go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/access"
)

// PluginName is the name of the AccessRuleOwnerEnforcement admission plugin.
const PluginName = "AccessRuleOwnerEnforcement"

// Plugin checks the owner field of writes to resources with an owner rule.
type Plugin struct {
	*admission.Handler
	rules *access.Rules
}

var (
	_ admission.ValidationInterface     = &Plugin{}
	_ admission.InitializationValidator = &Plugin{}
)

// NewPlugin returns the plugin. It needs the server's access rules, which
// SetAccessRules provides.
func NewPlugin() *Plugin {
	return &Plugin{Handler: admission.NewHandler(admission.Create, admission.Update)}
}

// SetAccessRules gives the plugin the server's access rules.
func (p *Plugin) SetAccessRules(r *access.Rules) { p.rules = r }

// ValidateInitialization implements admission.InitializationValidator.
func (p *Plugin) ValidateInitialization() error {
	if p.rules == nil {
		return fmt.Errorf("%s: missing access rules", PluginName)
	}
	return nil
}

// Validate implements admission.ValidationInterface.
func (p *Plugin) Validate(ctx context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error {
	if attrs.GetSubresource() != "" {
		// Status and other subresources can't change spec. A CRD whose owner
		// field a subresource could write is refused (see access.Declare).
		return nil
	}
	if attrs.GetResource().GroupResource() == crdResource {
		return validateCRD(ctx, attrs)
	}
	if _, inProject := milorequest.ProjectID(ctx); inProject {
		return nil
	}
	gr := attrs.GetResource().GroupResource()
	lookup := p.rules.Lookup(gr)
	switch lookup.State {
	case access.Unrestricted:
		return nil
	case access.Refused:
		// Like the access filter: an invalid or not-yet-trusted rule refuses
		// everyone but system:masters, who may repair the resource.
		if access.InPrivilegedGroup(attrs.GetUserInfo()) {
			return nil
		}
		return lookup.Err
	}
	rule := lookup.Rule
	owner, applies := access.RequestOwner(ctx)
	if !applies || rule.OwnerField == "" {
		return nil
	}
	if owner == "" {
		return apierrors.NewForbidden(gr, attrs.GetName(), fmt.Errorf("%s belong to users, and the request doesn't identify one", gr))
	}

	got, err := ownerOf(attrs.GetObject(), rule.OwnerField)
	if err != nil {
		return apierrors.NewInternalError(err)
	}
	switch attrs.GetOperation() {
	case admission.Create:
		if got != owner {
			return apierrors.NewForbidden(gr, attrs.GetName(),
				fmt.Errorf("%s must be %q, the user this request acts for", rule.OwnerField, owner))
		}
	case admission.Update:
		old, err := ownerOf(attrs.GetOldObject(), rule.OwnerField)
		if err != nil {
			return apierrors.NewInternalError(err)
		}
		if got != old {
			return apierrors.NewForbidden(gr, attrs.GetName(), fmt.Errorf("%s can't be changed", rule.OwnerField))
		}
	}
	return nil
}

// ownerOf reads the owner field of obj, or "" when it isn't set.
func ownerOf(obj runtime.Object, field string) (string, error) {
	if obj == nil {
		return "", nil
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", field, err)
		}
		u = &unstructured.Unstructured{Object: m}
	}
	v, ok := access.Field(u, field)
	if !ok || v == nil {
		return "", nil
	}
	return fmt.Sprint(v), nil
}
