# Signing in as a ServiceAccount with a client certificate

A controller that signs in to Milo with a client certificate can act as a Milo
ServiceAccount, so a project-scoped PolicyBinding to that ServiceAccount
decides what it may do. The controller needs no token or key: the certificate
carries the ServiceAccount's UID, and the ServiceAccount has to opt in.

## How Milo reads the certificate

The Kubernetes client-certificate authenticator takes the user name from the
subject's common name (CN), the groups from its organizations (O), and the UID
from the subject attribute `1.3.6.1.4.1.57683.2`. Milo's authorizer grants a
ServiceAccount by its UID, so the certificate's UID is what makes the request
count as the ServiceAccount.

Milo keeps a certificate's UID only when all of these hold:

- The request targets a project, and a ServiceAccount with that `metadata.uid`
  exists in that project's control plane. That is a request under
  `/apis/resourcemanager.miloapis.com/v1alpha1/projects/<project>/control-plane`.
  A request to the root, an organization or a user path has no project, so
  its certificate UID is always removed.
- The ServiceAccount has the annotation
  `iam.miloapis.com/client-certificate-authentication: Enabled`. Any other
  value, or no annotation, leaves it out.
- The ServiceAccount's `spec.state` is `Active` or empty, its `status.state`
  is not `Inactive`, and it is not being deleted. Any other state leaves it
  out.

Otherwise Milo removes the UID and the request continues as the certificate's
name and groups alone. A UID that names a User, a ServiceAccount in another
project or at organization level, or nothing at all is removed the same way.
A certificate UID counts only in the control plane that holds the
ServiceAccount, so a ServiceAccount bound in another project does not
authenticate by certificate UID there. The request is not
refused, so anything the certificate's name or groups are granted by RBAC still
works, but no PolicyBinding applies to it.

Milo lists a project's ServiceAccounts once and reuses the list for 30
seconds. A failed list is reused for 5 seconds, so an outage removes the UID
without a lookup on every request. Deleting the ServiceAccount, setting
it `Inactive` or removing the annotation stops its certificates within that
time. Deleting the PolicyBinding removes the access itself.

Every decision is counted in `milo_client_certificate_uid_decisions_total`,
labelled by `reason`: `Allowed`, `NoProject`, `NotFound`, `NotOptedIn`, `Inactive`,
`Deleting` or `LookupFailed`. The API server logs each removal once per
decision, with the project, the UID and the reason.

## Issuing a certificate

1. Create the ServiceAccount in the project and opt it in:

   ```yaml
   apiVersion: iam.miloapis.com/v1alpha1
   kind: ServiceAccount
   metadata:
     name: inventory-importer
     annotations:
       iam.miloapis.com/client-certificate-authentication: Enabled
   spec:
     state: Active
   ```

2. Read its UID from the project's control plane:

   ```shell
   kubectl get serviceaccounts.iam.miloapis.com inventory-importer \
     -o jsonpath='{.metadata.uid}'
   ```

3. Bind a role to the ServiceAccount with a PolicyBinding scoped to the
   project, as for any other ServiceAccount.

4. Issue a client certificate from the CA Milo trusts for client
   certificates, with the UID in the subject. With cert-manager, use a literal
   subject, because the structured `subject` fields cannot carry the
   attribute:

   ```yaml
   apiVersion: cert-manager.io/v1
   kind: Certificate
   metadata:
     name: inventory-importer
   spec:
     secretName: inventory-importer-client
     literalSubject: "CN=system:inventory-importer,1.3.6.1.4.1.57683.2=00000000-0000-4000-8000-000000000000"
     usages:
       - client auth
     duration: 24h
     issuerRef:
       kind: ClusterIssuer
       name: milo-client-ca
   ```

   With OpenSSL, put the attribute in the subject of the request:

   ```shell
   openssl req -new -key client.key -out client.csr \
     -subj "/CN=system:inventory-importer/1.3.6.1.4.1.57683.2=00000000-0000-4000-8000-000000000000"
   ```

   The subject must carry exactly one UID attribute. A certificate with two is
   refused at authentication.

5. Use the certificate against the project's control plane path. A request
   that targets another project, an organization, a user or the root control
   plane does not carry the ServiceAccount's identity: Milo removes the UID
   there.

## Renewal and recreation

Renewing the certificate keeps the UID, because the UID is part of the
certificate's configured subject, not of its key.

Recreating the ServiceAccount, even with the same name, gives it a new
`metadata.uid`. Certificates that carry the old UID stop counting as the
ServiceAccount. Update the certificate's subject with the new UID and reissue
it, and rebind the new ServiceAccount.
