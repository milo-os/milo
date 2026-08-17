# Test: `email-verification-policy`

Tests that the deny-unverified-email ValidatingAdmissionPolicy installs and
does not break ordinary writes.

WHAT THIS COVERS, and why it is worth a test on its own:

- The CEL compiles. The apiserver validates a policy's expressions when the
  policy object is created, so the apply step below IS the compile check.
- The policy evaluates without erroring at admission time. This is the case
  that matters most, because failurePolicy is Fail: an expression that
  compiles but errors on evaluation denies EVERY write in the cluster.
- An identity carrying no emailVerified key is admitted. That is the
  inertness guarantee the rollout plan depends on — the policy ships before
  zitadel-provider starts stamping the key, and must be a no-op until then.

WHAT THIS DOES NOT COVER, and nothing else currently does either: the deny
path. That needs an identity carrying iam.miloapis.com/emailVerified=false,
and the test infrastructure authenticates with static tokens, whose file
format (token,user,uid,"groups") has no field for extras. Reaching it would
mean impersonation via Impersonate-Extra-<key> headers, which kubectl
cannot express and which additionally needs RBAC on userextras/<key>.

So the denial itself is demonstrated by deployment, not by test. Worth
knowing before the binding is promoted from [Warn, Audit] to [Deny]: the
audit counter is what actually shows the policy matching the identities it
is meant to match.

NOTE the binding here uses validationActions [Deny], NOT the [Warn, Audit]
that ships in the datum repo. Under Warn a broken policy would not fail the
write, and this test would pass without proving anything.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [install-policy](#step-install-policy) | 0 | 2 | 0 | 0 | 0 |
| 2 | [ordinary-write-still-succeeds](#step-ordinary-write-still-succeeds) | 0 | 2 | 0 | 0 | 0 |

### Step: `install-policy`

Creating the policy is the CEL compile check — the apiserver rejects a
policy whose expressions do not type-check.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `sleep` | 0 | 0 | *No description* |

### Step: `ordinary-write-still-succeeds`

With the policy enforcing at Deny, an identity carrying no emailVerified
key must still be admitted. Fails if the expression errors at evaluation
(failurePolicy Fail turns that into a cluster-wide outage) or if absence
is ever read as unverified.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

