# Test: `unserved-requests-counter`

End-to-end test for the API server's counter of requests in a project for
types the project does not serve, in the default environment, where the
DiscoveryContextFilter feature gate is off.

A project-context LIST of a CRD tagged for the Organization context moves
projectstorage_unserved_requests_total{verb="list"} for that type by the
number of LIST requests the server received for it, which its own
apiserver_request_total records: the first request to a new project's
storage can answer 429 while its cache starts, and the gateway's route
retries 429s. A project-context LIST of configmaps, which every context
serves, moves nothing. The counter counts before storage, so the project
need not exist.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [install-unserved-crd](#step-install-unserved-crd) | 0 | 3 | 0 | 0 | 0 |
| 2 | [verify-counter](#step-verify-counter) | 0 | 1 | 0 | 0 | 0 |

### Step: `install-unserved-crd`

Install a CRD tagged for the Organization context, so a project does not serve it.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `sleep` | 0 | 0 | *No description* |

### Step: `verify-counter`

A project LIST of the unserved type counts once; a project LIST of configmaps does not count.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

---

