# Specification

Design document for `aws-account-provisioner`.

## 1. Overview

A single-binary CLI that provisions AWS accounts through **Control Tower Account Factory**
and assigns **IAM Identity Center** permission sets to groups and users.

Creating an AWS account is slow (20–40 minutes) and irreversible. This tool is built around
that constraint: everything is validated up front, several accounts are provisioned
concurrently, and assignments are applied as soon as each account becomes available.

## 2. Goals / Non-goals

### Goals

- Provision multiple accounts from a single TSV file
- Validate every referenced OU, permission set, group and user **before** anything is created
- Poll provisioning status concurrently and report progress
- Assign permission sets as soon as each account is ready
- Leave no infrastructure behind — the tool is a local CLI with no deployed components

### Non-goals

- Permissions Boundary enforcement / StackSet manipulation
- SCP management
- Account deletion, suspension, or moving accounts between OUs
- Renaming existing accounts
- Detecting duplicate root email addresses (AWS exposes no API for this — see §9)

## 3. Prerequisites

| Requirement | Detail |
|---|---|
| Go | 1.23 or later |
| AWS Control Tower | Enabled; Account Factory available as a Service Catalog product |
| IAM Identity Center | Enabled in the management account |
| AWS profile | An SSO profile able to assume a role in the Organizations management account |

## 4. Commands

```bash
# Interactive wizard — generates input.tsv
aws-account-provisioner --init

# Validate without creating anything
aws-account-provisioner --dry-run input.tsv

# Provision
aws-account-provisioner input.tsv
```

| Flag | Default | Description |
|---|---|---|
| `--init` | `false` | Launch the interactive wizard and write `input.tsv` |
| `--dry-run` | `false` | Validate input and AWS state; create nothing |
| `--profile` | `$AWS_PROFILE` | AWS profile used to reach the management account |

## 5. Input file

TSV, one row per assignment. Rows sharing an `AccountEmail` are grouped: the account is
created once, and every assignment for it is applied afterwards.

```tsv
AccountEmail	AccountName	OU	PrincipalType	PrincipalName	PermissionSetName
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	USER	taro	ReadOnlyAccess
aws+stg@example.com	stg-account	Staging (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
```

| Column | Description |
|---|---|
| `AccountEmail` | Root email address. Must be globally unique across all of AWS |
| `AccountName` | Account display name |
| `OU` | Target organizational unit, formatted as `Name (ou-xxxx-xxxxxxxx)` |
| `PrincipalType` | `GROUP` or `USER` |
| `PrincipalName` | Group or user name in IAM Identity Center |
| `PermissionSetName` | Permission set name |

## 6. Configuration

Read from environment variables, optionally via a `.env` file.

| Variable | Required | Description |
|---|---|---|
| `ROOT_ACCOUNT_ID` | yes | 12-digit ID of the Organizations management account |
| `ASSUME_ROLE_NAME` | no | Role assumed in the management account. Default: `AWSControlTowerExecution` |
| `EMAIL_TEMPLATE` | no | Template used by the wizard to derive root emails. Default: `aws+{account_name}@example.com` |
| `AWS_PROFILE` | no | Overridden by `--profile` |

`EMAIL_TEMPLATE` supports the `{account_name}` placeholder, substituted with the lowercased
account name.

## 7. Processing flow

```
authenticate → discover → validate → provision → poll → assign → report
```

1. **Authenticate** — assume `ASSUME_ROLE_NAME` in `ROOT_ACCOUNT_ID` using the given profile
2. **Discover** — locate the Account Factory product (Service Catalog) and the Identity Center
   instance; page through permission sets, resolving each ARN to its name once and caching the
   result; page through users, groups and OUs
3. **Validate** — check TSV shape, then confirm every OU, permission set, group and user exists
4. **Provision** — submit `ProvisionProduct` for every distinct account
5. **Poll** — one goroutine per account, 30-second interval, 60-minute ceiling
6. **Assign** — as each account reaches `AVAILABLE`, create its account assignments and wait for
   each to reach `SUCCEEDED`
7. **Report** — print a summary and write `results/YYYYMMDD_HHMMSS_result.tsv`

Steps 2 and 3 are the whole of `--dry-run`.

### Why cache permission sets

`ListPermissionSets` returns ARNs only, so each one needs a `DescribePermissionSet` call to
recover its name. Resolving lazily would issue that call on every lookup, so the mapping is
built once during discovery and held in memory.

## 8. Output

```
results/
└── 20260919_181530_result.tsv
```

| Column | Description |
|---|---|
| `OU` | Target OU |
| `AccountID` | Created account ID |
| `AccountName` | Account display name |
| `AccountEmail` | Root email |
| `Status` | `SUCCEEDED` / `FAILED` |
| `ErrorMessage` | Populated on failure |

A summary is also printed to stdout.

## 9. Error handling

| Case | Behaviour |
|---|---|
| Validation failure | Abort before any account is created; report every problem at once |
| Provisioning failure for one account | Other accounts continue; the failure is recorded |
| Assignment failure | The account is still reported as created, with the assignment error attached |
| Polling timeout (60 min) | Marked `FAILED`; AWS may still complete it, so the account ID is reported if known |
| `SIGINT` | Stop polling and exit cleanly. **Already-submitted `ProvisionProduct` requests continue on the AWS side** — this is printed explicitly as a warning |

Duplicate root email addresses cannot be detected in advance: AWS offers no dry-run for account
creation. Such an account fails at step 4 and is reported as `FAILED`.

## 10. Required IAM permissions

The assumed role needs, at minimum:

| Service | Actions |
|---|---|
| `organizations` | `ListRoots`, `ListOrganizationalUnitsForParent`, `DescribeAccount` |
| `servicecatalog` | `SearchProducts`, `DescribeProduct`, `ProvisionProduct`, `DescribeRecord` |
| `sso` (ssoadmin) | `ListInstances`, `ListPermissionSets`, `DescribePermissionSet`, `CreateAccountAssignment`, `DescribeAccountAssignmentCreationStatus` |
| `identitystore` | `ListUsers`, `ListGroups`, `GetUserId`, `GetGroupId` |
| `sts` | `AssumeRole` |

A ready-to-use policy document ships as `docs/iam-policy.json`.

## 11. Implementation

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| AWS | AWS SDK for Go v2 (`organizations`, `servicecatalog`, `ssoadmin`, `identitystore`, `sts`, `config`) |
| CLI parsing | Standard library `flag` |
| Interactive prompts | `promptui`, with incremental search on long lists |
| Configuration | `godotenv` |
| Tests | Standard library `testing` |
| Lint | `golangci-lint`, run in CI |
