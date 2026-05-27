// Package v1alpha1 contains API schema definitions for the networking.datumapis.com group.
//
// # Locations Overview
//
// The networking API group models the physical points-of-presence (PoPs) where
// platform services run and the consumer-facing projections that grant projects
// access to those PoPs.
//
// # Core Resource Types
//
// **Location**: A cluster-scoped record of a physical point-of-presence where
// platform services are deployed and reachable by consumer workloads. A Location
// is categorized by its ownership model (datum-managed, provider-dedicated, or
// self-managed) and carries human-readable metadata such as a display name, IATA
// city code, and region.
//
// **LocationBinding**: A namespace-scoped projection of a Location into a
// project's namespace. Bindings are the consumer-facing answer to "which
// locations does my project have access to?" and mirror the relevant fields of
// the canonical Location they reference.
//
// +k8s:deepcopy-gen=package,register
// +groupName=networking.datumapis.com
package v1alpha1
