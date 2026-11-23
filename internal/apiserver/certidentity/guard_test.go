package certidentity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	testingclock "k8s.io/utils/clock/testing"

	projctx "go.miloapis.com/milo/pkg/request"
)

const (
	saUID   = "03aefc68-de9c-4ae6-90fd-51ec13a1854c"
	userUID = "340583683847097652"
)

func serviceAccount(uid string, mutate ...func(*unstructured.Unstructured)) unstructured.Unstructured {
	sa := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "iam.miloapis.com/v1alpha1",
		"kind":       "ServiceAccount",
		"metadata":   map[string]any{"name": "controller"},
		"spec":       map[string]any{"state": "Active"},
	}}
	sa.SetUID(types.UID(uid))
	sa.SetAnnotations(map[string]string{OptInAnnotation: OptInValue})
	for _, m := range mutate {
		m(&sa)
	}
	return sa
}

func notOptedIn(sa *unstructured.Unstructured) { sa.SetAnnotations(nil) }

func optInValue(v string) func(*unstructured.Unstructured) {
	return func(sa *unstructured.Unstructured) { sa.SetAnnotations(map[string]string{OptInAnnotation: v}) }
}

func inactive(sa *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(sa.Object, "Inactive", "spec", "state")
}

func unknownState(sa *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(sa.Object, "Suspended", "spec", "state")
}

func noState(sa *unstructured.Unstructured) {
	unstructured.RemoveNestedField(sa.Object, "spec", "state")
}

func statusInactive(sa *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(sa.Object, "Inactive", "status", "state")
}

func deleting(sa *unstructured.Unstructured) {
	now := metav1.Now()
	sa.SetDeletionTimestamp(&now)
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name     string
		accounts []unstructured.Unstructured
		uid      string
		want     Reason
	}{
		{"opted-in active service account", []unstructured.Unstructured{serviceAccount(saUID)}, saUID, ReasonAllowed},
		{"no service account has the UID, as for a user", []unstructured.Unstructured{serviceAccount(saUID)}, userUID, ReasonNotFound},
		{"empty control plane", nil, saUID, ReasonNotFound},
		{"service account without the annotation", []unstructured.Unstructured{serviceAccount(saUID, notOptedIn)}, saUID, ReasonNotOptedIn},
		{"annotation with another value", []unstructured.Unstructured{serviceAccount(saUID, optInValue("true"))}, saUID, ReasonNotOptedIn},
		{"inactive in spec", []unstructured.Unstructured{serviceAccount(saUID, inactive)}, saUID, ReasonInactive},
		{"unknown state in spec", []unstructured.Unstructured{serviceAccount(saUID, unknownState)}, saUID, ReasonInactive},
		{"no state in spec", []unstructured.Unstructured{serviceAccount(saUID, noState)}, saUID, ReasonAllowed},
		{"inactive in status", []unstructured.Unstructured{serviceAccount(saUID, statusInactive)}, saUID, ReasonInactive},
		{"being deleted", []unstructured.Unstructured{serviceAccount(saUID, deleting)}, saUID, ReasonDeleting},
		{"matches the right one of several", []unstructured.Unstructured{serviceAccount("other", notOptedIn), serviceAccount(saUID)}, saUID, ReasonAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.accounts, tc.uid); got != tc.want {
				t.Errorf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}

func certificateUser(uid string) *user.DefaultInfo {
	return &user.DefaultInfo{
		Name:   "system:controller@example.miloapis.com",
		UID:    uid,
		Groups: []string{"controllers", user.AllAuthenticated},
		Extra:  map[string][]string{user.CredentialIDKey: {"X509SHA256=abc123"}},
	}
}

func authenticateAs(u user.Info) authenticator.Request {
	return authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
		return &authenticator.Response{User: u}, true, nil
	})
}

type fakeProjects map[string][]unstructured.Unstructured

func (f fakeProjects) lookup(calls *int) Lookup {
	return func(_ context.Context, project string) ([]unstructured.Unstructured, error) {
		*calls++
		return f[project], nil
	}
}

func authenticate(t *testing.T, g *Guard, u user.Info, project string) user.Info {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/apis/fleet.datumapis.com/v1alpha1/machines", nil)
	if project != "" {
		req = req.WithContext(projctx.WithProject(req.Context(), project))
	}
	resp, ok, err := g.Authenticator(authenticateAs(u)).AuthenticateRequest(req)
	if err != nil || !ok {
		t.Fatalf("AuthenticateRequest = %v, %v", ok, err)
	}
	return resp.User
}

func TestAuthenticatorKeepsUIDOfOptedInServiceAccountInItsProject(t *testing.T) {
	calls := 0
	g := NewGuard(fakeProjects{"datum-cloud": {serviceAccount(saUID)}}.lookup(&calls), time.Minute)
	got := authenticate(t, g, certificateUser(saUID), "datum-cloud")
	if got.GetUID() != saUID {
		t.Errorf("UID = %q, want %q", got.GetUID(), saUID)
	}
}

func TestAuthenticatorRemovesUID(t *testing.T) {
	projects := fakeProjects{
		"datum-cloud": {serviceAccount(saUID)},
		"":            {serviceAccount(saUID)},
		"opted-out":   {serviceAccount(saUID, notOptedIn)},
		"unknown":     {serviceAccount(saUID, unknownState)},
		"inactive":    {serviceAccount(saUID, inactive)},
	}
	cases := []struct {
		name    string
		uid     string
		project string
	}{
		{"user UID", userUID, "datum-cloud"},
		{"service account from another project", saUID, "other-project"},
		{"root request with an opted-in root service account", saUID, ""},
		{"service account not opted in", saUID, "opted-out"},
		{"inactive service account", saUID, "inactive"},
		{"service account with an unknown state", saUID, "unknown"},
		{"project name that is not a DNS subdomain", saUID, "../datum-cloud"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			g := NewGuard(projects.lookup(&calls), time.Minute)
			in := certificateUser(tc.uid)
			got := authenticate(t, g, in, tc.project)
			if got.GetUID() != "" {
				t.Errorf("UID = %q, want it removed", got.GetUID())
			}
			if got.GetName() != in.Name {
				t.Errorf("name = %q, want %q kept", got.GetName(), in.Name)
			}
			if len(got.GetGroups()) != len(in.Groups) {
				t.Errorf("groups = %v, want %v kept", got.GetGroups(), in.Groups)
			}
			if _, ok := got.(*user.DefaultInfo); !ok {
				t.Errorf("user is %T, want *user.DefaultInfo for the project context decorator", got)
			}
			if in.UID != tc.uid {
				t.Errorf("the delegate's user was changed")
			}
		})
	}
}

func TestAuthenticatorLeavesOtherIdentitiesAlone(t *testing.T) {
	calls := 0
	g := NewGuard(fakeProjects{}.lookup(&calls), time.Minute)

	token := &user.DefaultInfo{Name: "alice", UID: userUID}
	if got := authenticate(t, g, token, "datum-cloud"); got.GetUID() != userUID {
		t.Errorf("token UID = %q, want it kept", got.GetUID())
	}
	noUID := certificateUser("")
	if got := authenticate(t, g, noUID, "datum-cloud"); got != noUID {
		t.Errorf("certificate without a UID was rewritten")
	}
	if calls != 0 {
		t.Errorf("lookups = %d, want none for identities without a certificate UID", calls)
	}
}

func TestAuthenticatorPassesThroughFailures(t *testing.T) {
	calls := 0
	g := NewGuard(fakeProjects{}.lookup(&calls), time.Minute)
	boom := errors.New("bad certificate")
	failing := authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
		return nil, false, boom
	})
	if _, ok, err := g.Authenticator(failing).AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/", nil)); ok || !errors.Is(err, boom) {
		t.Errorf("AuthenticateRequest = %v, %v; want the delegate's failure", ok, err)
	}
	if g.Authenticator(nil) != nil {
		t.Errorf("a nil delegate must stay nil so authentication stays off")
	}
}

func TestDecisionsExpire(t *testing.T) {
	clock := testingclock.NewFakeClock(time.Now())
	projects := fakeProjects{"datum-cloud": {serviceAccount(saUID)}}
	calls := 0
	g := NewGuardWithClock(projects.lookup(&calls), DefaultTTL, clock)

	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonAllowed {
		t.Fatalf("before deletion = %s", got)
	}
	delete(projects, "datum-cloud")
	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonAllowed {
		t.Fatalf("within the TTL = %s, want the cached decision", got)
	}
	if calls != 1 {
		t.Errorf("lookups within the TTL = %d, want 1", calls)
	}
	clock.Step(DefaultTTL + time.Second)
	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonNotFound {
		t.Errorf("after the TTL = %s, want %s once the service account is gone", got, ReasonNotFound)
	}
}

func TestOneListServesEveryUIDInAProject(t *testing.T) {
	calls := 0
	other := "7d1b4c66-2e0f-4a39-8c71-5a9e3b0d4f44"
	g := NewGuard(fakeProjects{"datum-cloud": {serviceAccount(saUID), serviceAccount(other, notOptedIn)}}.lookup(&calls), time.Minute)
	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonAllowed {
		t.Errorf("first UID = %s", got)
	}
	if got := g.Check(context.Background(), "datum-cloud", other); got != ReasonNotOptedIn {
		t.Errorf("second UID = %s", got)
	}
	if got := g.Check(context.Background(), "datum-cloud", userUID); got != ReasonNotFound {
		t.Errorf("third UID = %s", got)
	}
	if calls != 1 {
		t.Errorf("lists = %d, want 1 for the project", calls)
	}
}

func TestConcurrentChecksShareOneList(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	g := NewGuard(func(context.Context, string) ([]unstructured.Unstructured, error) {
		calls.Add(1)
		<-release
		return []unstructured.Unstructured{serviceAccount(saUID)}, nil
	}, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g.Check(context.Background(), "datum-cloud", fmt.Sprintf("uid-%d", i))
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("lists = %d, want 1 for concurrent checks in one project", n)
	}
}

func TestLookupFailureRemovesUIDAndIsRetriedAfterFailureTTL(t *testing.T) {
	clock := testingclock.NewFakeClock(time.Now())
	calls := 0
	fail := true
	g := NewGuardWithClock(func(context.Context, string) ([]unstructured.Unstructured, error) {
		calls++
		if fail {
			return nil, errors.New("unavailable")
		}
		return []unstructured.Unstructured{serviceAccount(saUID)}, nil
	}, DefaultTTL, clock)

	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonLookupFailed {
		t.Fatalf("during the outage = %s", got)
	}
	fail = false
	clock.Step(FailureTTL - time.Second)
	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonLookupFailed {
		t.Errorf("within the failure TTL = %s, want the cached failure", got)
	}
	if calls != 1 {
		t.Errorf("lookups within the failure TTL = %d, want 1", calls)
	}
	clock.Step(2 * time.Second)
	if got := g.Check(context.Background(), "datum-cloud", saUID); got != ReasonAllowed {
		t.Errorf("after the failure TTL = %s, want a fresh lookup", got)
	}
	if calls != 2 {
		t.Errorf("lookups = %d, want 2", calls)
	}
}
