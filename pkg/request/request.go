// pkg/request/context.go
package request

import "context"

type ctxKey string

const projectKey ctxKey = "project-id"

func WithProject(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, projectKey, id)
}

func ProjectID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(projectKey).(string)
	return id, ok
}

const organizationKey ctxKey = "organization-id"

// WithOrganization stashes the organization ID on the context. It is the
// canonical setter used by the organization context router so downstream
// consumers (e.g. the quota admission plugin) can read it without depending
// on the heavier pkg/server/filters package.
func WithOrganization(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, organizationKey, id)
}

// OrganizationID returns the organization ID stashed on the context by
// WithOrganization, if any.
func OrganizationID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(organizationKey).(string)
	return id, ok
}
