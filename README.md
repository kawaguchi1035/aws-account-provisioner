# aws-account-provisioner

Provision AWS accounts through **Control Tower Account Factory** and assign
**IAM Identity Center** permission sets — in a single command.

> 🚧 **Work in progress.** The design is settled ([docs/SPEC.md](docs/SPEC.md)); implementation is underway.

## Why

Creating an AWS account takes 20–40 minutes and cannot be undone. Doing it by hand for a batch
of accounts means a long afternoon of console work, and a typo in a permission set name only
surfaces once the account already exists.

This tool inverts that. Everything — OUs, permission sets, groups, users — is validated before a
single account is created, provisioning runs concurrently, and assignments are applied the moment
each account becomes available.

## Features

- **Dry run first.** `--dry-run` validates the input file and confirms every referenced OU,
  permission set, group and user exists — creating nothing.
- **Batch provisioning.** Multiple accounts from one TSV, polled concurrently with live progress.
- **Assignments included.** Permission sets are attached to groups and users as each account
  lands, not as a separate manual step.
- **Interactive wizard.** `--init` walks you through account names, OU selection and assignments,
  then writes the input file for you.
- **No infrastructure.** A local binary. Nothing is deployed, nothing is left running.

## Install

```bash
go install github.com/kawaguchi1035/aws-account-provisioner@latest
```

## Usage

```bash
# 1. Generate an input file interactively
aws-account-provisioner --init

# 2. Validate everything without creating accounts
aws-account-provisioner --dry-run input.tsv

# 3. Provision
aws-account-provisioner input.tsv
```

### Input file

TSV, one row per assignment. Rows sharing an email are grouped — the account is created once.

```tsv
AccountEmail	AccountName	OU	PrincipalType	PrincipalName	PermissionSetName
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	USER	taro	ReadOnlyAccess
```

### Configuration

Set via environment variables or a `.env` file.

| Variable | Required | Default | Description |
|---|---|---|---|
| `ROOT_ACCOUNT_ID` | yes | — | Organizations management account ID |
| `ASSUME_ROLE_NAME` | no | `AWSControlTowerExecution` | Role assumed in the management account |
| `EMAIL_TEMPLATE` | no | `aws+{account_name}@example.com` | Root email pattern used by `--init` |
| `AWS_PROFILE` | no | — | Overridden by `--profile` |

## Requirements

- Go 1.23+
- AWS Control Tower enabled, with Account Factory available
- IAM Identity Center enabled
- An AWS profile that can assume a role in the management account
  (see [required permissions](docs/SPEC.md#10-required-iam-permissions))

## Caveats

- **Account creation is irreversible.** Always run `--dry-run` first.
- **Duplicate root emails cannot be pre-checked.** AWS provides no API for it, so a collision
  surfaces at provisioning time and is reported as a failure.
- **`Ctrl+C` stops polling, not provisioning.** Requests already sent to AWS continue on their
  side; the tool warns you when this happens.

## Documentation

- [docs/SPEC.md](docs/SPEC.md) — full specification

## License

[MIT](LICENSE)
