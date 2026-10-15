# Test: `policybinding-project-owner-gc`

Verifies that a PolicyBinding on a Project is removed with its Project, and
that the owner reference which does it never removes a live grant.

The PolicyBinding mutating webhook adds an owner reference to the Project a
binding's resourceRef names, only when the live Project carries the uid the
binding names. Milo's garbage collector deletes a dependent whose owner is
missing or carries another uid, so a reference to the wrong uid would delete
the binding at once.

This test verifies:
- A binding on a Project gets an owner reference to it, and is removed when
  the Project is deleted.
- A binding that names a uid the Project does not carry gets no owner
  reference, and stays.
- An update that strips the owner reference gets it back.

The webhook also gives no owner reference to a binding created through a
project control plane. A project control plane serves no PolicyBinding
today, so the unit tests cover that case.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup](#step-setup) | 0 | 3 | 0 | 0 | 0 |
| 2 | [create-kept-project](#step-create-kept-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [binding-gains-the-owner-reference](#step-binding-gains-the-owner-reference) | 0 | 5 | 0 | 0 | 0 |
| 4 | [binding-goes-with-its-project](#step-binding-goes-with-its-project) | 0 | 2 | 0 | 0 | 0 |
| 5 | [stale-uid-gets-no-reference](#step-stale-uid-gets-no-reference) | 0 | 4 | 0 | 0 | 0 |
| 6 | [update-restores-the-reference](#step-update-restores-the-reference) | 0 | 5 | 0 | 0 | 0 |

### Step: `setup`

Create the organization, the assignable role and the User the bindings grant it to

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |

### Step: `create-kept-project`

Create the Project that stays for the rest of the test

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `binding-gains-the-owner-reference`

A binding on a Project gets an owner reference to that Project

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 1 | *No description* |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `assert` | 0 | 0 | *No description* |

### Step: `binding-goes-with-its-project`

Deleting the Project removes the binding through the garbage collector

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `stale-uid-gets-no-reference`

A binding that names a uid the Project does not carry gets no owner
reference. With one, the garbage collector would see a uid mismatch and
delete it at once. The previous step shows how fast the collector acts.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `sleep` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

### Step: `update-restores-the-reference`

An update that strips the owner reference gets it back from the webhook

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 1 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `script` | 0 | 0 | *No description* |
| 5 | `assert` | 0 | 0 | *No description* |

---

