#!/usr/bin/env bash
# One-off backfill of the notification.miloapis.com/created-date label on
# Emails created before the Email mutating webhook started setting it.
# Each label is the UTC date of the Email's creationTimestamp, which is the
# only update the Email validator accepts on an existing Email.
#
# Runs against the current kubectl context (or KUBECONFIG). Safe to re-run:
# it only touches Emails without the label.
#
# Usage:
#   ./backfill-email-created-date.sh            # dry run: print what would be labelled
#   ./backfill-email-created-date.sh --apply    # label the Emails
set -euo pipefail

LABEL="notification.miloapis.com/created-date"
APPLY=false

case "${1:-}" in
  "") ;;
  --apply) APPLY=true ;;
  *) echo "usage: $0 [--apply]" >&2; exit 1 ;;
esac

# creationTimestamp comes back in UTC (RFC 3339 with a Z suffix), so its first
# ten characters are the label value.
kubectl get emails.notification.miloapis.com --all-namespaces \
  --selector "!${LABEL}" \
  --chunk-size 100 \
  --no-headers \
  --output custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,CREATED:.metadata.creationTimestamp |
  while read -r namespace name created; do
    date="${created:0:10}"
    if [ "$APPLY" = true ]; then
      kubectl label emails.notification.miloapis.com --namespace "$namespace" "$name" "${LABEL}=${date}"
    else
      echo "would label ${namespace}/${name} ${LABEL}=${date}"
    fi
  done
