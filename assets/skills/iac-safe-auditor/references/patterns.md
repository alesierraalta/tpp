# Infrastructure as Code (IaC) Testing Patterns

These examples target Terraform/OpenTofu with the AWS provider. Each proves the boundary it names and nothing more. Kubernetes, Helm and Dockerfiles are not covered here.

## 1. Terraform Test (v1.7+) with Mock Providers, including a negative control

`mock_provider` exists from Terraform 1.7 and OpenTofu 1.8 (verify against your installed version). It exercises module logic, conditionals, `for_each` and outputs without contacting AWS. Mocks generate placeholder values for computed attributes, so assert only on values derived from your own configuration, never on provider-computed fields.

A test that can only pass proves nothing. Pair every positive `run` with a `run` that feeds a bad input and uses `expect_failures`:

```hcl
# tests/s3_bucket.tftest.hcl
mock_provider "aws" {}

variables {
  bucket_name = "test-secure-audit-bucket"
  environment = "staging"
}

run "bucket_name_and_tags_are_derived_from_inputs" {
  command = plan

  assert {
    condition     = aws_s3_bucket.main.bucket == "test-secure-audit-bucket-staging"
    error_message = "Bucket name formatting failed"
  }

  assert {
    condition     = aws_s3_bucket.main.tags["Environment"] == "staging"
    error_message = "Missing or invalid Environment tag"
  }
}

# Negative control: the variable validation must reject an unknown environment.
run "unknown_environment_is_rejected" {
  command = plan

  variables {
    environment = "qa-typo"
  }

  expect_failures = [var.environment]
}
```

Encryption is checked by the policy layer (section 2), not by a mock assertion: the encryption algorithm lives in `aws_s3_bucket_server_side_encryption_configuration` (AWS provider v4+), and asserting a computed nested attribute under a mock is unreliable. Skip `terraform test` entirely for a module with no conditionals, `for_each` or variable validation; there is nothing for it to observe.

## 2. Rego policy over plan JSON, tested with `opa test`

`assets/conftest-policy.rego` uses OPA v1 syntax (`import rego.v1`, `deny contains msg if { ... }`). Its rules and what each one denies:

| Rule | Denies | Does not fire on |
| :--- | :--- | :--- |
| mandatory tags | taggable resources (plan has `tags` or `tags_all`) missing Environment/Owner/ManagedBy in `tags_all` merged with `tags` | `aws_security_group_rule`, policies, routes (no tag attribute); plans where `tags_all` is unknown and provider `default_tags` exist |
| public ingress | `0.0.0.0/0` or `::/0` reaching a sensitive port (22, 1433, 2375, 3306, 3389, 5432, 5984, 6379, 9200, 9300, 11211, 27017), including wide ranges, and `protocol = "-1"` (all traffic, message says verify) | HTTPS/other ports, private CIDRs, egress rules |
| S3 encryption | `aws_s3_bucket` with neither legacy inline encryption nor a linked `aws_s3_bucket_server_side_encryption_configuration` using `aws:kms` or `AES256` | correctly configured v4+ buckets, including an updated bucket whose encryption resource is `no-op` |
| IAM wildcard | Allow with `Action: "*"`; Allow with `Resource: "*"` plus a service-wide action such as `s3:*` | `ec2:Describe*` on `*`, scoped resources, Deny statements, unknown policies |
| version pinning | providers with no or unbounded (`>= x`) constraint; registry modules likewise; git modules without an immutable `ref` | `./` local modules, the built-in `terraform` provider |

All three ingress resource shapes (inline `ingress`, `aws_security_group_rule`, `aws_vpc_security_group_ingress_rule`) feed one guard, so switching resource type does not bypass it.

Run the policy tests, which include a deny and an allow fixture per rule:

```bash
opa test assets/ -v                    # or: conftest verify -p assets/
opa check --strict assets/             # syntax and unsafe-variable check
# Without a local binary: docker run --rm -v "$PWD/assets":/p openpolicyagent/opa:latest test /p
```

Prove the tests can fail: weaken one rule (for example remove `"::/0"` from `public_cidrs`) and confirm `opa test` goes red, then revert.

Run the policy against a real plan (the plan JSON is a secret, see SKILL.md rule 7):

```bash
terraform plan -out=tfplan.bin
terraform show -json tfplan.bin > "$(mktemp -d)/tfplan.json"   # keep outside the repo, delete after
# --rego-version v1 is needed only on Conftest builds that default to v0 (verify with conftest --version)
conftest test -p assets/ --rego-version v1 "$PATH_TO_TFPLAN_JSON"
```

Policy limits: only values known at plan time are visible; the S3 reference link resolves root-module references only; IAM policies built by `aws_iam_policy_document` data sources are visible only once their JSON is known in the plan; trust policies (`assume_role_policy` with `Principal: "*"`) are not covered.

## 3. LocalStack Provider Configuration (Endpoint-Isolated Emulation)

The endpoint override is the safety control. Keep egress blocked as an independent guard and never combine this with a real provider profile or account configuration. LocalStack covers only a subset of each AWS service and does not enforce IAM by default, so it cannot validate IAM semantics.

```hcl
# providers.tf for local testing
provider "aws" {
  access_key                  = "mock_key"
  secret_key                  = "mock_secret"
  region                      = "us-east-1"
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    s3       = "http://localhost:4566"
    sqs      = "http://localhost:4566"
    dynamodb = "http://localhost:4566"
    lambda   = "http://localhost:4566"
  }
}
```

## 4. Versioned FinOps Gate with Infracost

Do not compare an unlabelled report with an implicit budget. The gate needs a reproducible baseline, owner, tolerance, pricing context, and a fail-closed rule for unsupported or unpriced resources. Skip the gate for modules that create nothing priced.

```bash
BASELINE_VERSION="2025-01-15"
BUDGET_OWNER="platform-team"
TOLERANCE_USD="50"
UNPRICED_POLICY="fail"

# infracost-base.json is versioned and identified by BASELINE_VERSION.
test -s infracost-base.json || { echo "missing versioned baseline" >&2; exit 1; }
infracost diff --path . \
  --compare-to infracost-base.json \
  --format json \
  --out-file infracost-diff.json

jq --arg owner "$BUDGET_OWNER" --arg baseline "$BASELINE_VERSION" \
  --arg tolerance "$TOLERANCE_USD" --arg unpriced "$UNPRICED_POLICY" \
  '. + {budget_owner: $owner, baseline_version: $baseline,
        tolerance_usd: ($tolerance|tonumber), unsupported_policy: $unpriced}' \
  infracost-diff.json > infracost-gate-input.json
```

Unsupported or unpriced resources fail the gate unless the named owner records an explicit, reviewable exception.

## 5. Read-Only Drift Probe (needs real read-only credentials)

Drift is a live read of the real account. It cannot run inside the isolated sandbox of rule 2 and must be a separately authorized step: a human-approved run in a read-only role (no write, no `iam:PassRole`), against a named workspace, using the remote state backend's locking. A refresh takes the state lock, so run it when no apply is in flight.

```bash
terraform plan -refresh-only -detailed-exitcode
# 0: no difference; 2: drift detected; 1: refresh/plan error
```

`lifecycle { ignore_changes = [...] }` on a security attribute hides drift from this probe; grep the module for it before trusting exit code `0`. An HCP Terraform health assessment is a separate remote observation; record its status beside, not instead of, the local exit code. Neither signal authorizes `terraform apply`.

## Layer Limits

- `terraform validate` and TFLint find syntax and type/provider-lint issues, not organizational policy.
- Checkov and Trivy evaluate known misconfiguration patterns, not runtime behavior; use custom checks or Rego for organization-specific invariants and test them with a fixture that must fail.
- OPA over plan JSON cannot observe unknown values, `lifecycle.ignore_changes`, or behavior hidden by dynamic blocks; it also never sees state secrecy or backend encryption.
- `terraform test` mocks exercise module logic, not real provider response formats or computed values.
- LocalStack is subset emulation without IAM enforcement; Infracost is a list-price estimate.
- Drift probes report local plan state under whatever credentials were used, not the security of the account.
