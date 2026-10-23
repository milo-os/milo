package projectstate

import (
	"context"
	"strconv"

	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// liveLookups counts each Project read from storage, by why the view did not
// answer, whether storage answered with the Project, and whether the request
// reads or writes. It is the live lookup's cost, and it names the request
// class that a loopback GET's user agent cannot.
var liveLookups = k8smetrics.NewCounterVec(
	&k8smetrics.CounterOpts{
		Name: "projectstorage_project_lookups_total",
		Help: "Projects read from storage because the API server's view could not answer, " +
			"by reason (unseen or forced), found (false for NotFound or a failed read) and request (read or write)",
		StabilityLevel: k8smetrics.ALPHA,
	},
	[]string{"reason", "found", "request"},
)

func init() {
	legacyregistry.MustRegister(liveLookups)
}

// WriteVerb returns the request's verb when the request creates or changes an
// object: create, update or patch. Reads and deletes are served as before.
// Storage cannot tell them apart itself: a DELETE of an object with
// finalizers arrives as GuaranteedUpdate, as an update does, so the request's
// own verb decides.
func WriteVerb(ctx context.Context) (string, bool) {
	info, ok := genericapirequest.RequestInfoFrom(ctx)
	if !ok {
		return "", false
	}
	switch info.Verb {
	case "create", "update", "patch":
		return info.Verb, true
	}
	return "", false
}

// countLiveLookup counts one storage read of a Project. seen means the
// reflector held the name, so the read was forced by a recorded DELETE.
func countLiveLookup(ctx context.Context, seen bool, err error) {
	reason := "unseen"
	if seen {
		reason = "forced"
	}
	request := "read"
	if _, writes := WriteVerb(ctx); writes {
		request = "write"
	}
	liveLookups.WithLabelValues(reason, strconv.FormatBool(err == nil), request).Inc()
}
