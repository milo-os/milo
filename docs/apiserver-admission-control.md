# Admission control configuration

This page is for operators who deploy the Milo apiserver. It explains how to
give the webhook admission plugins credentials, such as a client certificate
for a webhook that requires mutual TLS (mTLS).

The Milo apiserver accepts `--admission-control-config-file`, the path to an
`AdmissionConfiguration` file with per-plugin configuration.

The flag configures plugins; it doesn't choose them. The plugin set and order
are fixed, so no argument can turn off quota, project suspension or webhook
enforcement. `--enable-admission-plugins`, `--disable-admission-plugins` and
the deprecated `--admission-control` aren't available.

When you leave the flag unset, the webhook plugins present no client
credentials.

## Give webhooks a client certificate

The `ValidatingAdmissionWebhook` and `MutatingAdmissionWebhook` plugins each
read a kubeconfig named in the `AdmissionConfiguration`. When a plugin calls a
webhook, it uses the kubeconfig user whose name matches the webhook's address:

- For a webhook with a `service` reference, the name is
  `<service>.<namespace>.svc:<port>`.
- For a webhook with a `url`, the name is the URL's `<host>:<port>`.
- A name such as `*.example-system.svc:443` matches any service in that
  namespace on that port.

A webhook whose address matches no user presents no client credentials, unless
the kubeconfig has a `*` user or a current context.

The webhook configuration's `caBundle` still verifies the webhook's serving
certificate. The kubeconfig only supplies the apiserver's client credentials.

### Example

This synthetic example gives the validating webhook plugin a client
certificate for a webhook served by the `example-webhook` service in the `example-system`
namespace on port 443.

`/etc/milo/admission/admission-config.yaml`:

```yaml
apiVersion: apiserver.config.k8s.io/v1
kind: AdmissionConfiguration
plugins:
  - name: ValidatingAdmissionWebhook
    configuration:
      apiVersion: apiserver.config.k8s.io/v1
      kind: WebhookAdmissionConfiguration
      kubeConfigFile: /etc/milo/admission/webhook-kubeconfig.yaml
```

`/etc/milo/admission/webhook-kubeconfig.yaml`:

```yaml
apiVersion: v1
kind: Config
users:
  - name: example-webhook.example-system.svc:443
    user:
      client-certificate: /etc/milo/pki/webhook-client/tls.crt
      client-key: /etc/milo/pki/webhook-client/tls.key
```

Include only the plugins that need credentials. A plugin without an entry runs
as it does without the flag. Mutating webhooks use the same entry with
`name: MutatingAdmissionWebhook`; the tests cover only the validating plugin.

Pass the flag as an ordinary container argument:

```yaml
args:
  - apiserver
  - --admission-control-config-file=/etc/milo/admission/admission-config.yaml
```

Mount both files and the certificate's Secret into the apiserver container at
the paths the files name. Keep the client key in a Secret, not in a ConfigMap
or in Git.

## Startup and changes

The apiserver reads the `AdmissionConfiguration` and the kubeconfig when it
starts. If it can't read or parse either file, it exits with an error; a
missing `AdmissionConfiguration` gives
`failed to apply admission: failed to read plugin config`. Restart the
apiserver after you change either file.

The apiserver reads the certificate and key files when it calls a webhook, not
at startup. If they're missing, calls to the matching webhooks fail, and the
webhook's `failurePolicy` decides whether the request is refused.
