# PlatformAccess Migration Controller

This controller automatically syncs the state of `User` resources to `PlatformAccess` resources during the transition from the legacy `PlatformAccessApproval` and `PlatformAccessRejection` models.

When a new `User` is created, a corresponding `PlatformAccess` resource is automatically generated. The controller continuously monitors the user's status (`State` and `RegistrationApproval`) and propagates any status updates (such as approval, rejection, or deactivation) to the matching fields on the `PlatformAccess` resource.

> [!NOTE]
> This migration controller is temporary and will be removed once the migration to the new `PlatformAccess` model is complete.
