// Package cockroach stores Kubernetes API objects in CockroachDB in place of
// etcd. It implements the API server's storage interface, so resources keep
// their usual reads, writes and watches.
//
// Each project has its own resource versions, and so do the shared
// resources that every project sees.
//
// [Migrate] prepares the database, [Open] connects a [Backend], and
// [Decorator] serves the API server's resources from it.
package cockroach
