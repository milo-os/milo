package engine

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apiserver/pkg/endpoints/request"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/templateutil"
)

// TemplateEngine handles ResourceClaim and ResourceGrant generation from policy templates.
type TemplateEngine interface {
	// RenderClaim renders a complete ResourceClaim from a ClaimCreationPolicy.
	RenderClaim(policy *quotav1alpha1.ClaimCreationPolicy, evalContext *EvaluationContext) (*quotav1alpha1.ResourceClaim, error)

	// RenderGrant renders a complete ResourceGrant from a GrantCreationPolicy.
	RenderGrant(policy *quotav1alpha1.GrantCreationPolicy, triggerObj *unstructured.Unstructured) (*quotav1alpha1.ResourceGrant, error)

	// EvaluateConditions evaluates trigger conditions against a resource object.
	EvaluateConditions(conditions []quotav1alpha1.ConditionExpression, obj *unstructured.Unstructured) (bool, error)
}

// EvaluationContext provides context for template evaluation in admission scenarios.
type EvaluationContext struct {
	Object      *unstructured.Unstructured
	User        UserContext
	RequestInfo *request.RequestInfo
	Namespace   string
	GVK         struct {
		Group   string
		Version string
		Kind    string
	}
}

// GrantEvaluationContext provides context for template evaluation in grant creation scenarios.
type GrantEvaluationContext struct {
	Object         *unstructured.Unstructured
	ParentContext  map[string]interface{}
	ResourceType   string
	ConsumerRef    quotav1alpha1.ConsumerRef
	RequestContext map[string]interface{}
}

// UserContext provides user information for template evaluation.
type UserContext struct {
	Name   string
	UID    string
	Groups []string
	Extra  map[string][]string
}

// templateEngine implements TemplateEngine.
type templateEngine struct {
	logger    logr.Logger
	celEngine CELEngine
}

// NewTemplateEngine creates a new template engine.
func NewTemplateEngine(celEngine CELEngine, logger logr.Logger) TemplateEngine {
	return &templateEngine{
		logger:    logger.WithName("template-engine"),
		celEngine: celEngine,
	}
}

// renderResourceClaimSpec renders a ResourceClaimTemplate into a ResourceClaimSpec.
func (e *templateEngine) renderResourceClaimSpec(template quotav1alpha1.ResourceClaimTemplate, evalContext *EvaluationContext) (*quotav1alpha1.ResourceClaimSpec, error) {
	// Build full context variables for ClaimCreationPolicy templates
	variables := e.buildClaimTemplateContext(evalContext)

	// Process resource requests using CEL expressions
	var resourceRequests []quotav1alpha1.ResourceRequest
	for _, requestTemplate := range template.Spec.Requests {
		// Render ResourceType using CEL template
		resourceType, err := e.renderCELTemplate(requestTemplate.ResourceType, variables)
		if err != nil {
			return nil, fmt.Errorf("failed to render ResourceType: %w", err)
		}

		// Use the amount directly from the template
		amount := requestTemplate.Amount

		resourceRequests = append(resourceRequests, quotav1alpha1.ResourceRequest{
			ResourceType: resourceType,
			Amount:       amount,
		})
	}

	// Render ConsumerRef using CEL
	consumerRef := template.Spec.ConsumerRef
	if template.Spec.ConsumerRef.Name != "" {
		renderedName, err := e.renderCELTemplate(template.Spec.ConsumerRef.Name, variables)
		if err != nil {
			return nil, fmt.Errorf("failed to render ConsumerRef.Name: %w", err)
		}
		consumerRef.Name = renderedName
	}

	return &quotav1alpha1.ResourceClaimSpec{
		Requests:    resourceRequests,
		ConsumerRef: consumerRef,
	}, nil
}

// renderClaimMetadata renders name/generateName/namespace and annotations for claim metadata.
func (e *templateEngine) renderClaimMetadata(metadata quotav1alpha1.ObjectMetaTemplate, evalContext *EvaluationContext) (string, string, string, map[string]string, map[string]string, error) {
	// Build full context variables for ClaimCreationPolicy templates
	variables := e.buildClaimTemplateContext(evalContext)

	// Render name
	name := ""
	if metadata.Name != "" {
		rendered, err := e.renderCELTemplate(metadata.Name, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render name: %w", err)
		}
		name = rendered
	}

	// Render generateName
	generateName := ""
	if metadata.GenerateName != "" {
		rendered, err := e.renderCELTemplate(metadata.GenerateName, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render generateName: %w", err)
		}
		generateName = rendered
	}

	// Render namespace (default to object namespace if not specified)
	namespace := evalContext.Namespace
	if metadata.Namespace != "" {
		rendered, err := e.renderCELTemplate(metadata.Namespace, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render namespace: %w", err)
		}
		namespace = rendered
	}

	// Render labels (literal values, not templates)
	labels := make(map[string]string)
	for key, value := range metadata.Labels {
		labels[key] = value
	}

	// Render annotations (support templates)
	annotations := make(map[string]string)
	for key, value := range metadata.Annotations {
		rendered, err := e.renderCELTemplate(value, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render annotation %q: %w", key, err)
		}
		annotations[key] = rendered
	}

	return name, generateName, namespace, labels, annotations, nil
}

// renderResourceGrantSpec renders a ResourceGrantTemplate into a ResourceGrantSpec.
func (e *templateEngine) renderResourceGrantSpec(template quotav1alpha1.ResourceGrantTemplate, evalContext *GrantEvaluationContext) (*quotav1alpha1.ResourceGrantSpec, error) {
	// Build context variables for GrantCreationPolicy templates (only trigger)
	variables := e.buildGrantTemplateContext(evalContext)

	// Process ConsumerRef
	consumerRef := template.Spec.ConsumerRef
	if consumerRef.Name != "" {
		renderedName, err := e.renderCELTemplate(consumerRef.Name, variables)
		if err != nil {
			return nil, fmt.Errorf("failed to render ConsumerRef.Name: %w", err)
		}
		consumerRef.Name = renderedName
	}

	// Process Allowances (currently just copy them as-is, but could add templating in future)
	allowances := make([]quotav1alpha1.Allowance, len(template.Spec.Allowances))
	copy(allowances, template.Spec.Allowances)

	return &quotav1alpha1.ResourceGrantSpec{
		ConsumerRef: consumerRef,
		Allowances:  allowances,
	}, nil
}

// renderGrantMetadata renders name/generateName/namespace and annotations for grant metadata.
func (e *templateEngine) renderGrantMetadata(metadata quotav1alpha1.ObjectMetaTemplate, evalContext *GrantEvaluationContext) (string, string, string, map[string]string, map[string]string, error) {
	// Build context variables for GrantCreationPolicy templates (only trigger)
	variables := e.buildGrantTemplateContext(evalContext)

	// Render name
	name := ""
	if metadata.Name != "" {
		rendered, err := e.renderCELTemplate(metadata.Name, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render name: %w", err)
		}
		name = rendered
	}

	// Render generateName
	generateName := ""
	if metadata.GenerateName != "" {
		rendered, err := e.renderCELTemplate(metadata.GenerateName, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render generateName: %w", err)
		}
		generateName = rendered
	}

	// Render namespace
	namespace := ""
	if metadata.Namespace != "" {
		rendered, err := e.renderCELTemplate(metadata.Namespace, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render namespace: %w", err)
		}
		namespace = rendered
	}

	// Render labels (literal values, not templates)
	labels := make(map[string]string)
	for key, value := range metadata.Labels {
		labels[key] = value
	}

	// Render annotations (support templates)
	annotations := make(map[string]string)
	for key, value := range metadata.Annotations {
		rendered, err := e.renderCELTemplate(value, variables)
		if err != nil {
			return "", "", "", nil, nil, fmt.Errorf("failed to render annotation %q: %w", key, err)
		}
		annotations[key] = rendered
	}

	return name, generateName, namespace, labels, annotations, nil
}

// renderCELTemplate renders a template string using CEL expressions with templateutil.
func (e *templateEngine) renderCELTemplate(templateStr string, variables map[string]interface{}) (string, error) {
	if templateStr == "" {
		return "", nil
	}

	// Check if the template contains expressions
	hasExpr, err := templateutil.ContainsExpression(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to check for expressions in template %q: %w", templateStr, err)
	}

	// If no expressions, return the literal string
	if !hasExpr {
		return templateStr, nil
	}

	// Split the template into segments
	segments, err := templateutil.Split(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", templateStr, err)
	}

	// Render each segment
	var result strings.Builder
	for _, segment := range segments {
		if segment.Expression {
			// Evaluate CEL expression
			value, err := e.celEngine.EvaluateTemplateExpression(segment.Value, variables)
			if err != nil {
				return "", fmt.Errorf("failed to evaluate CEL expression %q: %w", segment.Value, err)
			}
			result.WriteString(value)
		} else {
			// Literal text
			result.WriteString(segment.Value)
		}
	}

	return result.String(), nil
}

// buildClaimTemplateContext creates CEL evaluation context for ClaimCreationPolicy templates.
// Includes trigger, user, and requestInfo variables.
func (e *templateEngine) buildClaimTemplateContext(evalContext *EvaluationContext) map[string]interface{} {
	variables := map[string]interface{}{}

	// Always include trigger object
	if evalContext.Object != nil {
		variables["trigger"] = evalContext.Object.Object
	}

	// Include user context if available
	if evalContext.User.Name != "" || evalContext.User.UID != "" || len(evalContext.User.Groups) > 0 || len(evalContext.User.Extra) > 0 {
		variables["user"] = map[string]interface{}{
			"name":   evalContext.User.Name,
			"uid":    evalContext.User.UID,
			"groups": evalContext.User.Groups,
			"extra":  evalContext.User.Extra,
		}
	}

	// Include requestInfo if available
	if evalContext.RequestInfo != nil {
		variables["requestInfo"] = map[string]interface{}{
			"verb":              evalContext.RequestInfo.Verb,
			"resource":          evalContext.RequestInfo.Resource,
			"subresource":       evalContext.RequestInfo.Subresource,
			"namespace":         evalContext.RequestInfo.Namespace,
			"name":              evalContext.RequestInfo.Name,
			"apiGroup":          evalContext.RequestInfo.APIGroup,
			"apiVersion":        evalContext.RequestInfo.APIVersion,
			"isResourceRequest": evalContext.RequestInfo.IsResourceRequest,
			"path":              evalContext.RequestInfo.Path,
			"parts":             evalContext.RequestInfo.Parts,
		}
	}

	return variables
}

// buildGrantTemplateContext creates CEL evaluation context for GrantCreationPolicy templates.
// Includes only the trigger variable.
func (e *templateEngine) buildGrantTemplateContext(evalContext *GrantEvaluationContext) map[string]interface{} {
	variables := map[string]interface{}{}

	// Always include trigger object
	if evalContext.Object != nil {
		variables["trigger"] = evalContext.Object.Object
	}

	return variables
}

// EvaluateConditions delegates to the CEL engine to evaluate trigger conditions.
func (e *templateEngine) EvaluateConditions(conditions []quotav1alpha1.ConditionExpression, obj *unstructured.Unstructured) (bool, error) {
	return e.celEngine.EvaluateConditions(conditions, obj)
}

// RenderGrant renders a complete ResourceGrant from a GrantCreationPolicy.
func (e *templateEngine) RenderGrant(policy *quotav1alpha1.GrantCreationPolicy, triggerObj *unstructured.Unstructured) (*quotav1alpha1.ResourceGrant, error) {
	// Create evaluation context for grant rendering
	evalContext := &GrantEvaluationContext{
		Object:       triggerObj,
		ResourceType: triggerObj.GetKind(),
	}

	// Render the grant spec
	spec, err := e.renderResourceGrantSpec(policy.Spec.Target.ResourceGrantTemplate, evalContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render grant spec: %w", err)
	}

	// Render metadata including namespace from template
	name, generateName, namespace, labels, annotations, err := e.renderGrantMetadata(policy.Spec.Target.ResourceGrantTemplate.Metadata, evalContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render grant metadata: %w", err)
	}

	// If no name was specified in template, generate a default one
	if name == "" {
		name = fmt.Sprintf("%s-%s-grant", policy.Name, triggerObj.GetName())
	}

	// Create the ResourceGrant object
	grant := &quotav1alpha1.ResourceGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:         name,
			GenerateName: generateName,
			Namespace:    namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: *spec,
	}

	// Set TypeMeta
	grant.APIVersion = "quota.miloapis.com/v1alpha1"
	grant.Kind = "ResourceGrant"

	return grant, nil
}

// RenderClaim renders a complete ResourceClaim from a ClaimCreationPolicy.
func (e *templateEngine) RenderClaim(policy *quotav1alpha1.ClaimCreationPolicy, evalContext *EvaluationContext) (*quotav1alpha1.ResourceClaim, error) {
	// Render the claim spec
	spec, err := e.renderResourceClaimSpec(policy.Spec.Target.ResourceClaimTemplate, evalContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render claim spec: %w", err)
	}

	// Render metadata including namespace from template
	name, generateName, namespace, labels, annotations, err := e.renderClaimMetadata(policy.Spec.Target.ResourceClaimTemplate.Metadata, evalContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render claim metadata: %w", err)
	}

	// If no name was specified in template, generate a default one
	if name == "" && generateName == "" {
		// Use generateName with policy name as prefix
		generateName = fmt.Sprintf("%s-claim-", policy.Name)
	}

	// Create the ResourceClaim object
	claim := &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:         name,
			GenerateName: generateName,
			Namespace:    namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: *spec,
	}

	// Set TypeMeta
	claim.APIVersion = "quota.miloapis.com/v1alpha1"
	claim.Kind = "ResourceClaim"

	return claim, nil
}
