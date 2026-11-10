# MiloQuotaClaimWaitsHigh and MiloQuotaClaimWaitGetsAfterGone

## What These Alerts Mean

A create of a quota-tracked resource waits in admission until its ResourceClaim is granted or denied. Each wait holds
one watch on that claim, for at most 30 seconds. A wait normally ends in about a second.

**MiloQuotaClaimWaitsHigh** fires when one API server has held more open waits than the rule's threshold for 10
minutes. Nothing in admission limits concurrent waits: a non-exempt caller is held back by API Priority and
Fairness, because a waiting create keeps its seat, but an exempt caller is held back only by the 30 seconds. So many
open waits mean that claims are judged slowly, or that a caller creates in bulk faster than they are judged.

**MiloQuotaClaimWaitGetsAfterGone** fires when the claim watches of one API server often answer `410 Gone`. A wait
watches from the version of its own create, so a 410 means that the claim cache no longer holds that version, for
example because the cache started again. After a 410 the wait reads its claim, at most three times.

Both alerts fire per API server pod.

## Impact

Creates of quota-tracked resources take longer, and fail with "Your request took too long to be checked against your
quota" when a wait reaches 30 seconds. Each open wait costs the API server memory and goroutines; the rule's comment
gives the measured cost and the level at which one API server slowed down.

## Investigation Steps

### 1. Are claims judged?

The claim controller runs in the controller manager. Check that it runs and that new claims become Granted or Denied:

```sh
kubectl get resourceclaims -A --sort-by=.metadata.creationTimestamp | tail
```

Claims that stay Pending point at the controller manager: its pods, its logs, its quota controllers' errors.

### 2. Who creates?

`apiserver_request_total{verb="POST"}` by user agent and resource, and `milo_quota_admission_waiter_registrations_total`,
show whether one caller creates in bulk.

### 3. For 410s: did the claim cache start again?

`apiserver_watch_cache_initializations_total{resource="resourceclaims"}` on the same pod shows a cache start. A
rollout or a node roll starts every cache cold.

## Resolution

The waits alert resolves when open waits fall below the threshold; the 410 alert when the watches stop answering 410.
