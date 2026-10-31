## Unreleased — each tenant's spend series in one read

- `BillingService.ListTenantTimeSeries` returns the spend series of up to 50
  named tenants, in the order named, from one sum of the ledger across them:
  a trend beside each tenant a list shows. It is the platform admin's, as
  `GetPlatformTimeSeries` is.
- The dashboard's budget alerts draw a 30-day spend trend beside each tenant.
