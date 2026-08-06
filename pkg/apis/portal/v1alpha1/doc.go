// Package v1alpha1 contains API schema definitions for the portal.miloapis.com group.
//
// # Portal Plugin System Overview
//
// The portal plugin system lets a service register its own UI as a Module
// Federation remote, loaded at runtime by one of Datum Cloud's portal hosts
// (cloud-portal, the customer-facing portal; staff-portal, the internal
// operator portal) instead of that host's own engineers hand-coding the
// integration.
//
// # Core Resource Types
//
// **[ConsumerPortalPlugin](#consumerportalplugin)**: Registers a plugin consumed by
// cloud-portal. Its extensions are scoped to a project (a sidebar nav item, a
// routed page under a project, a card on the project home page).
//
// **[ProviderPortalPlugin](#providerportalplugin)**: Registers a plugin consumed by
// staff-portal. Its extensions are platform-scoped (no project/organization
// context) — a top-level nav item, a platform-wide routed page, or a
// declared resource type that staff-portal's own Resources list renders
// without running any plugin code at all.
//
// Both Kinds are written only by the services-operator, fanned out from a
// ServiceConfiguration's spec.userInterface block — service teams declare
// their plugin in ServiceConfiguration, not by creating these resources
// directly.
//
// # Why Two Kinds Instead of One
//
// A single PortalPlugin Kind with an audience field was considered and
// rejected: the two hosts' extension contracts genuinely diverge (project-
// scoped vs. platform-scoped extension types), and each host should only
// need to watch its own Kind — no client-side filtering, and each contract
// can evolve independently as cloud-portal and staff-portal's plugin models
// diverge further.
//
// +k8s:deepcopy-gen=package,register
// +groupName=portal.miloapis.com
package v1alpha1
