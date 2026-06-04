![Hero image](docs/images/hero-D.png#gh-dark-mode-only)
![Hero image](docs/images/hero-L.png#gh-light-mode-only)

Milo is a "system of action" for product-led, B2B companies. Think of
it like a control plane for modern service providers, built on top of a
comprehensive system of record that ties together key parts of your business.

## Why We're Building Milo

Over the last two decades scaling infrastructure clouds (Voxel, Packet,
SoftLayer, StackPath), we've spent a lot of time building or stitching together
the pieces required to run a company at some decent scale: understanding our
contacts, users, accounts, usage, quotes, contracts, agreements, etc.

While a number of awesome vertical tools have emerged to solve particular pain
points (like authorization, metering, billing, or SOC2 compliance), fast-growing
companies have a large "back office" surface area to maintain and very little
go-to-market (GTM) tooling suitable for omni-channel cloud motions. Inevitably, each tool
needs foundational, trusted data upon which to act, creating a competing "system
of record" environment. The emergence of AI agents makes this even more
clear.

As we set out to build [Datum Cloud](https://www.datum.net) (an infrastructure
cloud optimized for network and data sensitive workloads), we were driven to
help a new class of service providers gain hyperscaler advantages. We decided
that instead of simply using the lessons we'd learned over the years to build
our own kick-butt back office, we should make it available to others. Et voila!

## Quick Start

Get Milo running locally in under 5 minutes:

```bash
# Prerequisites: Docker, Kind, kubectl, and Task installed
git clone https://github.com/milo-os/milo.git
cd milo

# Enable remote task files to be used
export TASK_X_REMOTE_TASKFILES=1
task dev:setup
```

This deploys a complete Milo environment with API server, storage, and
controllers. Access it with:

```bash
export KUBECONFIG=.milo/kubeconfig
kubectl get organizations
```

📚 **[Full setup guide →](docs/getting-started.md)**
📖 **[API documentation →](docs/api/)**
🧱 **[Manual migrations →](docs/migrations/README.md)**

## What We Prefer Not to Build

Projects with such a wide surface area can engender a "build everything"
mindset. While our vision calls for a pretty comprehensive approach, there are a
number of capabilities that are either commoditized or serviced by existing
scaled vendors. Here are some examples:

- Email sending can be provided by Twilio, Resend, etc.
- Authentication can be provided by Zitadel, Clerk, Auth0, Descope, etc.
- General automation can be provided by Zapier, Workato, Make, etc
- Product analytics and visualization can be provided by PostHog, Grafana, etc.
- User enrichment can be provided by Clay, Apollo, Clearbit, etc.
- Payments can be provided by Stripe, Adyen, etc.
- Tax and financial compliance can be provided by Avalara, NetSuite, etc.

## What We're Starting With

There are a few big "System of Record" buckets to which we think folks should
have programmatic access, namely: contacts, accounts, products, vendors, and
assets.

- Operator Portal: Hosted admin panel for a "single pane of glass" business
  view.
- Contacts: Marketing contacts management with dynamic lists and opt-in.
- Customers: User, Account, Parent Account management w/ standard workflows.
- Staff Management: A source of truth for RBAC and related workflows.
- Vendor Profiles: Supplier profiles with related documents.
- Fraud & Abuse: Basic risk and fraud scoring for user sign ups
- Agreements: Online and offline management of AuP, ToS, MSA, NDA, etc.
- Audit Logs: Unified cross platform event and audit logs.
- Product Catalog: Programmable foundation for billing, quoting, feature access.
- Pricing: Transparent pricing models tailored for scalability and flexibility.
- Entitlements: Management of feature access, quotas and tiering.

## Future Capabilities

We see integrated commercial functionality as the big unlock for scale. Here are
some areas we're planning to work on:

- Privacy: GDPR policy management with sub-processor and change notifications
- Deal Rooms: Hosted trust centers for quotes, policies, & agreements
- Quoting: Generate and manage quotes for streamlined sales engagements.
- Order Management: A workflow for contract and order lifecycle management.
- Purchase Orders: An API to support end user procurement tracking
- Billing: A centralized hub for account statements, two sided ledger, & invoice
  status
